//go:build linux

package provision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/disk/luks"
	"github.com/telekom/BOOTy/pkg/executil"
	"github.com/telekom/BOOTy/pkg/image"
)

// Disk encryption (docs/disk-encryption.md), format first. stream-image
// writes the image up to the encrypted partition, hands a random recovery
// key to the provisioner, grows the partition to the disk end, formats it
// with LUKS2 (slot 0 recovery key, slot 1 a one-time enrollment key), opens
// it and streams the partition content into the mapping. The steps after it
// use the mapping as the root. prepare-disk-encryption writes crypttab, the
// dracut configuration, the enrollment key and the first-boot unit into the
// target and rebuilds its initramfs; close-encrypted-disk closes the
// mapping after teardown-chroot. On the first boot with Secure Boot on, the
// unit binds the volume to the TPM2 (PCR 7+14) and rebuilds the initramfs
// without the enrollment key; on the next boot, which the TPM2 unlocked, it
// wipes slot 1.
const (
	luksMapperName    = "booty_crypt"
	luksEnrollKeyPath = "/etc/booty/luks-enroll.key"
	luksEnvPath       = "/etc/booty/luks-tpm2.env"
	luksScriptPath    = "/usr/local/sbin/booty-luks-tpm2-enroll"
	luksUnitName      = "booty-luks-tpm2-enroll.service"
	luksDracutConf    = "/etc/dracut.conf.d/90-booty-luks.conf"
	luksDracutKeyConf = "/etc/dracut.conf.d/91-booty-luks-enroll-key.conf"
	luksGrubConf      = "/etc/default/grub.d/99-booty-luks.cfg"
	luksGrubLock      = "/etc/grub.d/01_booty_luks_lock"
	luksStatusAuth    = "/etc/booty/luks-status.auth"
	luksStatusCA      = "/etc/booty/luks-status-ca.pem"
	// luksTPM2PCRs: PCR 7 is the Secure Boot state, PK, KEK, db, dbx and the
	// authorities that verified the boot images, also a MOK when it
	// verified one. PCR 14 adds what PCR 7 does not see: MOKs that only
	// sign modules, MokListX and MokSBState. The cost: every MOK, MokListX
	// (shim update with a vendor dbx) or MokSBState change needs a new
	// binding, like db, dbx and KEK changes for PCR 7.
	luksTPM2PCRs     = "7+14"
	luksRecoverySlot = 0
	luksEnrollSlot   = 1
)

// The libraries that systemd-cryptsetup and systemd-cryptenroll dlopen for
// TPM2 (src/shared/tpm2-util.c), and the libcryptsetup token plugin that
// systemd-cryptsetup unlocks the TPM2 token with. dracut does not see dlopen
// dependencies; its tpm2-tss module, which would add the plugin, needs
// tpm2-tools.
var luksTPM2Libs = []string{
	"libtss2-esys.so.0", "libtss2-rc.so.0", "libtss2-mu.so.0", "libtss2-tcti-device.so.0",
	"cryptsetup/libcryptsetup-token-systemd-tpm2.so",
}

// The binaries that the installed system needs, relative to its root.
var luksTargetTools = []string{
	"usr/bin/systemd-cryptenroll", "usr/lib/systemd/systemd-cryptsetup",
	"usr/sbin/cryptsetup", "usr/bin/dracut", "usr/bin/lsinitrd", "usr/bin/shred",
}

// efivarsDir and sysTPMDir; variables for tests.
var (
	efivarsDir = "/sys/firmware/efi/efivars"
	sysTPMDir  = "/sys/class/tpm/tpm0"
	errNoTPM2  = errors.New("systemd-cryptenroll needs a TPM 2.0")
)

const efiGlobalGUID = "8be4df61-93ca-11d2-aa0d-00e098032b8c"

// encryptionExtras are the inputs of writeEncryptionFiles besides the keys.
type encryptionExtras struct {
	libs      []string
	hostname  string
	statusURL string
	token     string // bearer token for statusURL
	ca        []byte // CA_BUNDLE_FILE, for statusURL
	grubHash  string // unusable GRUB superuser password (pbkdf2)
}

// withEncryptionSteps adds the disk encryption steps; without
// DISK_ENCRYPTION the step list is the same as without this feature.
// prepare-disk-encryption runs before configure-grub, which picks up its
// GRUB files; close-encrypted-disk needs the target unmounted.
func (o *Orchestrator) withEncryptionSteps(steps []Step) []Step {
	out := make([]Step, 0, len(steps)+2)
	for _, st := range steps {
		switch st.Name {
		case "configure-grub":
			out = append(out, Step{"prepare-disk-encryption", o.prepareDiskEncryption})
		case "report-success":
			out = append(out, Step{"close-encrypted-disk", o.closeEncryptedDisk})
		}
		out = append(out, st)
	}
	return out
}

// errDiskEncryption marks a stream-image failure of disk encryption.
var errDiskEncryption = errors.New("disk encryption")

// encryptionState is what stream-image hands to the later steps.
type encryptionState struct {
	device    string // the LUKS2 partition
	luksUUID  string
	enrollKey []byte
	isRoot    bool
}

func (o *Orchestrator) encryptionEnabled() bool {
	return o.cfg.Provision.DiskEncryption.Enabled()
}

// encryptionActive reports whether this run encrypts: enabled, and not
// skipped by DISK_ENCRYPTION_ALLOW_FAILURE.
func (o *Orchestrator) encryptionActive() bool {
	return o.encryption != nil
}

// streamEncryptedImage is stream-image with encryption. A failed check
// before the first disk write provisions without encryption with
// DISK_ENCRYPTION_ALLOW_FAILURE.
func (o *Orchestrator) streamEncryptedImage(ctx context.Context, url string, opts []image.StreamOpts) error {
	o.encryption = nil
	// Config validation rejects these; never write a layout in plain text.
	if o.cfg.Provision.Disk.PartitionLayout != nil || o.isABImageMode() || strings.EqualFold(o.cfg.Provision.Image.Mode, "partition") {
		return &PermanentError{Err: fmt.Errorf("%w: only whole-disk images can be encrypted", errDiskEncryption)}
	}
	num, err := o.encryptionPreflight()
	if err != nil {
		if o.cfg.Provision.DiskEncryption.AllowFailure {
			o.log.Warn("disk encryption is not possible, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)", "error", err)
			o.encryptionSkipped = true
			return image.Stream(ctx, url, o.targetDisk, opts...)
		}
		return &PermanentError{Err: fmt.Errorf("%w preflight: %w", errDiskEncryption, err)}
	}
	st := &encryptionState{enrollKey: make([]byte, 64), isRoot: num == 0}
	if st.luksUUID, err = randomUUID(); err != nil {
		return err
	}
	if _, err := rand.Read(st.enrollKey); err != nil {
		return fmt.Errorf("generate enrollment key: %w", err)
	}
	o.log.Info("streaming image into a LUKS2 partition", "url", image.RedactURL(url), "disk", o.targetDisk, "luksUUID", st.luksUUID)
	err = image.StreamEncryptedRoot(ctx, url, o.targetDisk, image.EncryptedRoot{
		Number: num,
		Open: func(ctx context.Context, number int, size int64) (string, error) {
			return o.formatEncryptedPartition(ctx, st, number, size)
		},
	}, opts...)
	if errors.Is(err, image.ErrEncryptedRootUnsupported) && o.cfg.Provision.DiskEncryption.AllowFailure {
		// Refused before the first disk write.
		clear(st.enrollKey)
		o.log.Warn("the image layout cannot be encrypted, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)", "error", err)
		o.encryptionSkipped = true
		return image.Stream(ctx, url, o.targetDisk, opts...)
	}
	if err != nil {
		clear(st.enrollKey)
		_ = runCryptsetup(context.Background(), nil, nil, "close", luksMapperName)
		// A retry would find the partition formatted already.
		return &PermanentError{Err: fmt.Errorf("%w: %w", errDiskEncryption, classifyImageStreamError(url, err))}
	}
	o.encryption = st
	return nil
}

// formatEncryptedPartition grows partition number to the disk end, escrows
// a new recovery key, formats the partition and opens it. The recovery key
// reaches the provisioner before the partition is formatted.
func (o *Orchestrator) formatEncryptedPartition(ctx context.Context, st *encryptionState, number int, size int64) (string, error) {
	st.device = disk.PartitionDevicePath(o.targetDisk, number)
	if out, err := o.disk.Run(ctx, "partprobe", o.targetDisk); err != nil {
		return "", fmt.Errorf("partprobe %s: %s: %w", o.targetDisk, strings.TrimSpace(string(out)), err)
	}
	if err := o.disk.GrowPartition(ctx, o.targetDisk, number); err != nil {
		return "", err
	}
	if err := waitForBlockDevice(ctx, st.device); err != nil {
		return "", err
	}
	if err := runCryptsetup(ctx, nil, nil, "isLuks", st.device); err == nil {
		return "", fmt.Errorf("%s is already LUKS; an interrupted run needs a new provisioning", st.device)
	}
	recovery, err := newRecoveryKey()
	if err != nil {
		return "", err
	}
	defer clear(recovery)
	if err := o.escrowRecoveryKey(ctx, st, recovery); err != nil {
		return "", err
	}
	// Both keys are random 256 bit or more; a slow KDF adds nothing.
	kdf := make([]string, 0, 5)
	kdf = append(kdf, "--pbkdf", "pbkdf2", "--pbkdf-force-iterations", "1000")
	if err := runCryptsetup(ctx, recovery, nil, append([]string{"luksFormat", "--type", "luks2", "--uuid", st.luksUUID,
		"--key-slot", strconv.Itoa(luksRecoverySlot), "--key-file", "-", "--batch-mode"}, append(kdf, st.device)...)...); err != nil {
		return "", err
	}
	if err := addEnrollKey(ctx, st, recovery, kdf); err != nil {
		return "", err
	}
	if err := runCryptsetup(ctx, recovery, nil, "open", "--key-file", "-", st.device, luksMapperName); err != nil {
		return "", fmt.Errorf("the recovery key does not open %s: %w", st.device, err)
	}
	mapped := luks.MappedPath(luksMapperName)
	n, err := blockDeviceSize(mapped)
	if err != nil {
		return "", err
	}
	if n < size {
		return "", fmt.Errorf("the LUKS2 mapping of %s holds %d bytes, the partition of the image needs %d: the disk is too small", st.device, n, size)
	}
	o.log.Info("partition formatted with LUKS2", "device", st.device, "luksUUID", st.luksUUID,
		"recoverySlot", luksRecoverySlot, "enrollSlot", luksEnrollSlot)
	return mapped, nil
}

// useEncryptedRoot runs after parse-partitions: the root is the mapping.
func (o *Orchestrator) useEncryptedRoot() error {
	if o.encryption == nil {
		if o.encryptionEnabled() && !o.encryptionSkipped {
			return errors.New("disk encryption cannot resume an interrupted run (the keys are gone); reprovision")
		}
		return nil
	}
	if o.rootPartition == o.encryption.device {
		o.encryption.isRoot = true // also when selected by number
	}
	if !o.encryption.isRoot {
		return nil
	}
	if o.rootPartition != o.encryption.device {
		return fmt.Errorf("BOOTy found the root at %s but encrypted %s; set DISK_ENCRYPTION_TARGET to the root partition number", o.rootPartition, o.encryption.device)
	}
	o.rootPartition = luks.MappedPath(luksMapperName)
	return nil
}

// prepareDiskEncryption checks the image and writes the unlock
// configuration into the target root. The disk is encrypted already, so a
// failed check fails the run.
func (o *Orchestrator) prepareDiskEncryption(ctx context.Context) error {
	st := o.encryption
	if st == nil {
		return nil
	}
	if st.isRoot && !o.separateBootMounted {
		clear(st.enrollKey)
		return errors.New("the root needs a separate /boot partition: GRUB cannot read the kernel from an encrypted root")
	}
	if err := checkEncryptionTarget(newroot, o.cfg.Provision.DiskEncryption.StatusURL != ""); err != nil {
		clear(st.enrollKey)
		return err
	}
	extras, err := o.encryptionExtras()
	if err == nil {
		err = writeEncryptionFiles(newroot, st, extras)
	}
	if err == nil {
		err = o.rewriteEncryptedFstab(ctx)
	}
	clear(st.enrollKey) // on disk in the encrypted root now
	if err != nil {
		return err
	}
	if out, err := o.disk.ChrootRun(ctx, newroot, "dracut --force --regenerate-all"); err != nil {
		return fmt.Errorf("rebuild initramfs with the LUKS unlock: %s: %w", strings.TrimSpace(string(out)), err)
	}
	o.log.Info("disk encryption prepared", "device", st.device, "luksUUID", st.luksUUID, "tpm2PCRs", luksTPM2PCRs)
	return nil
}

// rewriteEncryptedFstab points fstab entries that name the encrypted
// partition itself (device path, PARTUUID, PARTLABEL) at the mapping. A
// UUID= or LABEL= of the file system keeps working through the mapping.
func (o *Orchestrator) rewriteEncryptedFstab(ctx context.Context) error {
	path := filepath.Join(newroot, "etc", "fstab")
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return nil //nolint:nilerr // no fstab to rewrite
	}
	data, err := os.ReadFile(path) //nolint:gosec // regular file below the target root
	if err != nil {
		return fmt.Errorf("read fstab: %w", err)
	}
	out, changed := rewriteFstabSources(string(data), func(src string) bool {
		return o.resolveFstabSource(ctx, src) == o.encryption.device
	})
	if !changed {
		return nil
	}
	o.log.Info("fstab: the encrypted partition mounts through its mapping")
	if err := writeTargetRootFile(newroot, path, []byte(out)); err != nil {
		return fmt.Errorf("write fstab: %w", err)
	}
	return nil
}

// rewriteFstabSources replaces partition sources (device path, PARTUUID,
// PARTLABEL) for which isEncrypted is true with the mapping.
func rewriteFstabSources(fstab string, isEncrypted func(string) bool) (string, bool) {
	var b strings.Builder
	changed := false
	for line := range strings.Lines(fstab) {
		f := strings.Fields(line)
		if len(f) > 1 && !strings.HasPrefix(f[0], "#") && namesPartition(f[0]) && isEncrypted(f[0]) {
			line = strings.Replace(line, f[0], luks.MappedPath(luksMapperName), 1)
			changed = true
		}
		b.WriteString(line)
	}
	return b.String(), changed
}

// closeEncryptedDisk closes the mapping after teardown-chroot. A data
// partition grows here; resize-filesystem grows the root.
func (o *Orchestrator) closeEncryptedDisk(ctx context.Context) error {
	st := o.encryption
	if st == nil {
		return nil
	}
	mapped := luks.MappedPath(luksMapperName)
	if !st.isRoot {
		if err := o.disk.CheckExtFilesystemPreen(ctx, mapped); err != nil {
			return err
		}
		if err := o.disk.ResizeFilesystem(ctx, mapped, ""); err != nil {
			return err
		}
	}
	if isMountPoint(newroot) {
		return errors.New("the target root is still mounted")
	}
	return runCryptsetup(ctx, nil, nil, "close", luksMapperName)
}

// encryptionPreflight returns 0 for the root or the partition number, or
// why encryption is not possible. It runs before the disk is written.
func (o *Orchestrator) encryptionPreflight() (int, error) {
	num, err := o.cfg.Provision.DiskEncryption.TargetPartition()
	if err != nil {
		return 0, err
	}
	if v, err := os.ReadFile(filepath.Join(sysTPMDir, "tpm_version_major")); err != nil || strings.TrimSpace(string(v)) != "2" {
		return 0, fmt.Errorf("no TPM 2.0 (%s/tpm_version_major %q): %w", sysTPMDir, strings.TrimSpace(string(v)), errors.Join(err, errNoTPM2))
	}
	if !o.cfg.Provision.DiskEncryption.SkipSecureBootCheck {
		if err := secureBootUserMode(efivarsDir); err != nil {
			return 0, fmt.Errorf("%w; the volume would stay bound to the enrollment key (DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK)", err)
		}
	}
	for _, tool := range []string{"cryptsetup", "partprobe", "growpart", "e2fsck", "resize2fs"} {
		if _, err := executil.LookPath(tool); err != nil {
			return 0, fmt.Errorf("ramdisk: %w", err)
		}
	}
	if _, ok := o.provider.(config.RecoveryKeyEscrow); !ok {
		return 0, errors.New("the provider cannot hand over the recovery key")
	}
	return num, nil
}

// waitForBlockDevice waits up to 10 s for device after a partition table
// change. A variable for tests.
var waitForBlockDevice = func(ctx context.Context, device string) error {
	for i := 0; ; i++ {
		if _, err := os.Stat(device); err == nil {
			return nil
		}
		if i == 100 {
			return fmt.Errorf("%s did not appear", device)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s: %w", device, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// blockDeviceSize returns the size of a block device in bytes. A variable
// for tests.
var blockDeviceSize = func(device string) (int64, error) {
	f, err := os.Open(device) //nolint:gosec // the LUKS mapping
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", device, err)
	}
	defer f.Close() //nolint:errcheck // read only
	n, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, fmt.Errorf("size of %s: %w", device, err)
	}
	return n, nil
}

// secureBootUserMode checks that the firmware supports Secure Boot and is
// in user mode (a PK is enrolled), so Secure Boot can be turned on. In
// setup mode anybody can enroll keys and PCR 7 means nothing.
func secureBootUserMode(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "SecureBoot-"+efiGlobalGUID)); err != nil {
		return fmt.Errorf("the firmware has no Secure Boot: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SetupMode-"+efiGlobalGUID))
	if err != nil {
		return fmt.Errorf("read SetupMode: %w", err)
	}
	if len(data) < 5 {
		return fmt.Errorf("SetupMode has %d bytes", len(data))
	}
	if data[4] != 0 {
		return errors.New("the firmware is in Secure Boot setup mode (no PK)")
	}
	return nil
}

// encryptionExtras collects the status callback inputs and an unusable
// GRUB superuser password.
func (o *Orchestrator) encryptionExtras() (*encryptionExtras, error) {
	d := &o.cfg.Provision.DiskEncryption
	e := &encryptionExtras{libs: luksTargetLibs(newroot), hostname: o.cfg.Hostname, statusURL: d.StatusURL}
	if e.statusURL != "" {
		if escrow, ok := o.provider.(config.RecoveryKeyEscrow); ok {
			e.token = escrow.CurrentToken()
		}
		if f := o.cfg.Transport.CABundleFile; f != "" {
			ca, err := os.ReadFile(f) //nolint:gosec // CA_BUNDLE_FILE from the vars
			if err != nil {
				return nil, fmt.Errorf("read CA_BUNDLE_FILE for the status callback: %w", err)
			}
			e.ca = ca
		}
	}
	hash, err := unusableGrubHash()
	if err != nil {
		return nil, err
	}
	e.grubHash = hash
	return e, nil
}

// unusableGrubHash is a GRUB pbkdf2 entry of random salt and hash: no
// password matches it, so nobody can log in as the superuser.
func unusableGrubHash() (string, error) {
	b := make([]byte, 128)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate GRUB lock: %w", err)
	}
	return fmt.Sprintf("grub.pbkdf2.sha512.10000.%X.%X", b[:64], b[64:]), nil
}

// checkEncryptionTarget checks that the image can unlock the volume with
// dracut and systemd-cryptsetup and bind it with systemd-cryptenroll.
func checkEncryptionTarget(root string, curl bool) error {
	var missing []string
	tools := luksTargetTools
	if curl {
		tools = append(slices.Clone(tools), "usr/bin/curl")
	}
	for _, rel := range tools {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			missing = append(missing, "/"+rel)
		}
	}
	found := luksTargetLibs(root)
	for _, lib := range luksTPM2Libs {
		if !strings.Contains(strings.Join(found, " "), "/"+lib) {
			missing = append(missing, lib)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the image lacks %s (Ubuntu: dracut, systemd-cryptsetup, libtss2-rc0t64; see docs/disk-encryption.md)", strings.Join(missing, ", "))
	}
	return nil
}

// luksTargetLibs returns the TPM2 libraries of the image, as paths in it.
func luksTargetLibs(root string) []string {
	var libs []string
	for _, lib := range luksTPM2Libs {
		for _, dir := range []string{"usr/lib/*-linux-gnu", "usr/lib64", "usr/lib"} {
			if m, _ := filepath.Glob(filepath.Join(root, dir, lib)); len(m) > 0 {
				libs = append(libs, strings.TrimPrefix(m[0], root))
				break
			}
		}
	}
	return libs
}

func writeEncryptionFiles(root string, st *encryptionState, e *encryptionExtras) error {
	crypttab, err := luksCrypttab(root, st.luksUUID)
	if err != nil {
		return err
	}
	type file struct {
		path string
		data []byte
		mode os.FileMode
	}
	statusCA := ""
	if len(e.ca) > 0 {
		statusCA = luksStatusCA
	}
	files := []file{
		{luksEnrollKeyPath, st.enrollKey, 0o400},
		{"/etc/crypttab", crypttab, 0o644},
		// Sourced by the unit; config validation keeps quotes out.
		{luksEnvPath, []byte(fmt.Sprintf("LUKS_UUID='%s'\nLUKS_NAME='%s'\nLUKS_ENROLL_SLOT='%d'\nTPM2_PCRS='%s'\n"+
			"HOST='%s'\nSTATUS_URL='%s'\nSTATUS_CA='%s'\n",
			st.luksUUID, luksMapperName, luksEnrollSlot, luksTPM2PCRs, e.hostname, e.statusURL, statusCA)), 0o644},
		// A host-only build adds tpm2-tss for tpm2-device= in crypttab,
		// which needs tpm2-tools, and then drops systemd-cryptsetup. The
		// generic build (the Ubuntu default) with the libraries listed here
		// does not.
		{luksDracutConf, []byte("# Written by BOOTy: unlock the LUKS2 volume in the initramfs (docs/disk-encryption.md).\n" +
			"hostonly=\"no\"\n" +
			"add_dracutmodules+=\" crypt systemd-cryptsetup \"\n" +
			"install_items+=\" /etc/crypttab " + strings.Join(e.libs, " ") + " \"\n"), 0o644},
		{luksDracutKeyConf, []byte(luksDracutKeyConfText), 0o644},
		{luksGrubConf, []byte(luksGrubDefaults), 0o644},
		{luksGrubLock, []byte(luksGrubLockScript(e.grubHash)), 0o755},
		{luksScriptPath, []byte(luksEnrollScript), 0o755},
		{"/etc/systemd/system/" + luksUnitName, []byte(luksEnrollUnitText(e.statusURL != "")), 0o644},
	}
	if e.statusURL != "" {
		files = append(files, file{luksStatusAuth, statusAuthHeader(e.token), 0o600})
		if statusCA != "" {
			files = append(files, file{statusCA, e.ca, 0o644})
		}
	}
	for _, f := range files {
		if err := writeTargetRootFileMode(root, filepath.Join(root, f.path), f.data, f.mode); err != nil {
			return fmt.Errorf("write %s: %w", f.path, err)
		}
	}
	wants := filepath.Join(root, "etc", "systemd", "system", "multi-user.target.wants")
	if err := ensureTargetParentWithinRoot(root, wants); err != nil {
		return err
	}
	if err := os.MkdirAll(wants, 0o755); err != nil {
		return fmt.Errorf("enable %s: %w", luksUnitName, err)
	}
	link := filepath.Join(wants, luksUnitName)
	_ = os.Remove(link)
	if err := os.Symlink("/etc/systemd/system/"+luksUnitName, link); err != nil {
		return fmt.Errorf("enable %s: %w", luksUnitName, err)
	}
	return nil
}

// statusAuthHeader is the curl -H @file content; empty without a token.
func statusAuthHeader(token string) []byte {
	if token == "" {
		return nil
	}
	return []byte("Authorization: Bearer " + token + "\n")
}

const luksDracutKeyConfText = "# Written by BOOTy: removed by " + luksUnitName + " after the TPM2 enrollment.\n" +
	"install_items+=\" " + luksEnrollKeyPath + " \"\n"

// luksGrubDefaults: Ubuntu cloud images boot without initramfs by the root
// PARTUUID first, which cannot open the volume. Recovery entries boot into
// a root shell after the TPM2 unlocked the root.
const luksGrubDefaults = "# Written by BOOTy (docs/disk-encryption.md).\n" +
	"GRUB_FORCE_PARTUUID=\nGRUB_DISABLE_RECOVERY=true\n"

// luksGrubLockScript is a grub.d script: a superuser with an unusable
// password locks the editor and the GRUB shell, so nobody changes the
// kernel command line at the console (PCR 8 and 9 are not bound). The
// entries stay bootable: grub-mkconfig puts $menuentry_id_option, set by
// 00_header, on every menuentry and submenu line.
func luksGrubLockScript(hash string) string {
	return "#!/bin/sh\n# Written by BOOTy (docs/disk-encryption.md): lock the GRUB editor and shell.\n" +
		"cat <<'EOF'\nset superusers=\"booty-locked\"\npassword_pbkdf2 booty-locked " + hash + "\n" +
		"set menuentry_id_option=\"--unrestricted $menuentry_id_option\"\nEOF\n"
}

// luksCrypttab returns the target crypttab with the BOOTy volume, unlocked
// by the enrollment key until the first-boot unit switches it to the TPM2.
// x-initrd.attach makes dracut include the entry in a host-only initramfs.
func luksCrypttab(root, luksUUID string) ([]byte, error) {
	var b strings.Builder
	path := filepath.Join(root, "etc", "crypttab")
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		data, err := os.ReadFile(path) //nolint:gosec // regular file below the target root
		if err != nil {
			return nil, fmt.Errorf("read crypttab: %w", err)
		}
		for line := range strings.Lines(string(data)) {
			if f := strings.Fields(line); len(f) > 0 && f[0] == luksMapperName {
				continue
			}
			b.WriteString(strings.TrimRight(line, "\n") + "\n")
		}
	}
	fmt.Fprintf(&b, "%s UUID=%s %s luks,x-initrd.attach\n", luksMapperName, luksUUID, luksEnrollKeyPath)
	return []byte(b.String()), nil
}

func (o *Orchestrator) escrowRecoveryKey(ctx context.Context, st *encryptionState, recovery []byte) error {
	escrow, ok := o.provider.(config.RecoveryKeyEscrow)
	if !ok {
		return errors.New("the provider cannot hand over the recovery key")
	}
	body, err := recoveryKeyBody(o.cfg.Hostname, st, recovery)
	if err != nil {
		return err
	}
	defer clear(body)
	if err := escrow.EscrowRecoveryKey(ctx, body); err != nil {
		return fmt.Errorf("hand over the recovery key: %w", err)
	}
	o.log.Info("recovery key handed over", "device", st.device, "luksUUID", st.luksUUID)
	return nil
}

// recoveryKeyBody builds the escrow JSON without encoding/json for the key,
// whose pooled buffers would keep a copy that cannot be zeroed.
func recoveryKeyBody(hostname string, st *encryptionState, recovery []byte) ([]byte, error) {
	head, err := json.Marshal(struct {
		Hostname string `json:"hostname"`
		Device   string `json:"device"`
		LUKSUUID string `json:"luksUUID"`
		KeySlot  int    `json:"keySlot"`
	}{hostname, st.device, st.luksUUID, luksRecoverySlot})
	if err != nil {
		return nil, fmt.Errorf("encode recovery key metadata: %w", err)
	}
	// One allocation that fits the key, so append never leaves a copy of it
	// in a discarded buffer.
	if len(head) > 4096 || len(recovery) > 256 {
		return nil, errors.New("recovery key metadata too large")
	}
	body := make([]byte, 0, len(head)+len(recovery)+20)
	body = append(body, head[:len(head)-1]...)
	body = append(body, `,"recoveryKey":"`...)
	body = append(body, recovery...) // modhex and dashes: nothing to escape
	return append(body, `"}`...), nil
}

// addEnrollKey adds the enrollment key to slot 1. The key goes through a
// memfd, so it never has a path on a file system of the ramdisk.
func addEnrollKey(ctx context.Context, st *encryptionState, recovery []byte, kdf []string) error {
	fd, err := unix.MemfdCreate("booty-luks-enroll", unix.MFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("memfd for the enrollment key: %w", err)
	}
	f := os.NewFile(uintptr(fd), "booty-luks-enroll")
	defer f.Close() //nolint:errcheck // read-only use by the child
	if _, err := f.Write(st.enrollKey); err != nil {
		return fmt.Errorf("write enrollment key to memfd: %w", err)
	}
	// ExtraFiles[0] is fd 3 in cryptsetup; opening /proc/self/fd/3 starts
	// at offset 0. The ramdisk has no /dev/fd.
	args := append([]string{"luksAddKey", "--key-file", "-", "--key-slot", strconv.Itoa(luksEnrollSlot), "--batch-mode"}, kdf...)
	return runCryptsetup(ctx, recovery, f, append(args, st.device, "/proc/self/fd/3")...)
}

// runCryptsetup runs cryptsetup with key material on stdin only, never in
// the arguments. Its output holds no key. A variable for tests.
var runCryptsetup = func(ctx context.Context, stdin []byte, extra *os.File, args ...string) error {
	cmd := executil.CommandContext(ctx, "cryptsetup", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	if extra != nil {
		cmd.ExtraFiles = []*os.File{extra}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cryptsetup %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return nil
}

// newRecoveryKey returns 256 random bits in the format of systemd-cryptenroll
// --recovery-key: modhex (keyboard layout independent) in 8 groups of 8.
func newRecoveryKey() ([]byte, error) {
	const modhex = "cbdefghijklnrtuv"
	raw := make([]byte, 32)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate recovery key: %w", err)
	}
	key := make([]byte, 0, 71)
	for i, b := range raw {
		if i > 0 && i%4 == 0 {
			key = append(key, '-')
		}
		key = append(key, modhex[b>>4], modhex[b&0xf])
	}
	return key, nil
}

func randomUUID() (string, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", fmt.Errorf("generate LUKS UUID: %w", err)
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:]), nil
}

// luksEnrollUnitText runs the enrollment once Secure Boot is on, so PCR 7
// and PCR 14 have their final values (also after a MOK enrollment boot).
// With a status URL it waits for the network.
func luksEnrollUnitText(network bool) string {
	after := "local-fs.target cryptsetup.target"
	wants := ""
	if network {
		after += " network-online.target"
		wants = "Wants=network-online.target\n"
	}
	return `# Written by BOOTy (docs/disk-encryption.md).
[Unit]
Description=Bind the BOOTy LUKS2 volume to the TPM2
ConditionSecurity=uefi-secureboot
ConditionPathExists=` + luksEnrollKeyPath + `
After=` + after + `
` + wants + `
[Service]
Type=oneshot
ExecStart=` + luksScriptPath + `

[Install]
WantedBy=multi-user.target
`
}

// luksEnrollScript has two phases.
//
// Phase 1 (first boot with Secure Boot on in user mode): bind to the TPM2,
// prove that the TPM2 opens the volume, switch crypttab to the TPM2, build
// every initramfs without the enrollment key, check it and swap it in.
// Until that commit point the EXIT trap restores the enrollment unlock.
//
// Phase 2 (the next boot): the initramfs of this boot has no enrollment
// key and the TPM2 opens the volume with the PCRs of this boot, so the TPM2
// opened the root. Only then key slot 1 and the key are destroyed. A crash
// after luksKillSlot finds the slot gone and only cleans up.
const luksEnrollScript = `#!/bin/sh
# Written by BOOTy (docs/disk-encryption.md).
set -eu
. ` + luksEnvPath + `
dev=/dev/disk/by-uuid/$LUKS_UUID
key=` + luksEnrollKeyPath + `
keyconf=` + luksDracutKeyConf + `
bound=/etc/booty/luks-tpm2.bound
say() { echo "booty-luks-tpm2-enroll: $*"; }
efivar() { od -An -tu1 -j4 -N1 "/sys/firmware/efi/efivars/$1-` + efiGlobalGUID + `" 2>/dev/null | tr -d ' '; }
# Best effort; the token stays in a file, never in an argument.
status() {
	[ -n "$STATUS_URL" ] || return 0
	msg=$(printf '%s' "$2" | tr -d '"\\' | tr '\n' ' ' | cut -c1-300)
	code=$(curl -sS --proto =https --max-time 30 --retry 5 --retry-connrefused --retry-delay 5 \
		${STATUS_CA:+--cacert "$STATUS_CA"} -H @` + luksStatusAuth + ` -H 'Content-Type: application/json' \
		-o /dev/null -w '%{http_code}' \
		--data "{\"hostname\":\"$HOST\",\"luksUUID\":\"$LUKS_UUID\",\"status\":\"$1\",\"message\":\"$msg\"}" \
		"$STATUS_URL") || code=000
	case $code in 2??) ;; *) say "status $1 not delivered (HTTP $code)" ;; esac
}
crypttab_line() { sed -i "s|^$LUKS_NAME .*|$LUKS_NAME UUID=$LUKS_UUID $1|" /etc/crypttab; }
fail() { say "failed: $*"; status failed "$*"; exit 1; }

if [ "$(efivar SecureBoot)" != 1 ] || [ "$(efivar SetupMode)" != 0 ]; then
	say "Secure Boot is not on in user mode, the enrollment waits"
	exit 0
fi

if [ -e "$bound" ]; then
	list=$(lsinitrd "/boot/initrd.img-$(uname -r)") || fail "lsinitrd of the booted initramfs"
	case $list in *luks-enroll.key*) fail "the initramfs of this boot still has the enrollment key" ;; esac
	case $list in *libcryptsetup-token-systemd-tpm2.so*) ;; *) fail "the initramfs of this boot lacks the TPM2 token plugin" ;; esac
	grep -q "^$LUKS_NAME UUID=$LUKS_UUID none .*tpm2-device=auto" /etc/crypttab || fail "crypttab does not unlock with the TPM2"
	cryptsetup open --test-passphrase --token-only --token-type systemd-tpm2 "$dev" || fail "the TPM2 does not open $dev with the PCRs of this boot"
	if cryptsetup luksDump "$dev" | grep -q "^  $LUKS_ENROLL_SLOT: luks2"; then
		cryptsetup open --test-passphrase --key-slot "$LUKS_ENROLL_SLOT" --key-file "$key" "$dev" ||
			fail "key slot $LUKS_ENROLL_SLOT does not hold the enrollment key"
		cryptsetup -q luksKillSlot "$dev" "$LUKS_ENROLL_SLOT"
	else
		say "key slot $LUKS_ENROLL_SLOT is already gone"
	fi
	shred -u "$key"
	rm -f "$bound" /boot/initrd.img-*.booty-old
	systemctl disable ` + luksUnitName + `
	status slot1-removed "key slot $LUKS_ENROLL_SLOT and the enrollment key are gone; the TPM2 (PCR $TPM2_PCRS) and the recovery key unlock"
	rm -f ` + luksStatusAuth + `
	say "done: key slot $LUKS_ENROLL_SLOT and the enrollment key are gone, the TPM2 (PCR $TPM2_PCRS) and the recovery key unlock $dev"
	exit 0
fi

step=start
# An interrupted earlier phase 1 left originals behind: restore them first.
for old in /boot/initrd.img-*.booty-old; do
	[ -e "$old" ] && mv "$old" "${old%.booty-old}"
done
restore() {
	# Images that a failed or interrupted swap replaced come back.
	for old in /boot/initrd.img-*.booty-old; do
		[ -e "$old" ] && mv "$old" "${old%.booty-old}"
	done
	crypttab_line "$key luks,x-initrd.attach"
	printf '%s\ninstall_items+=" %s "\n' "# Written by BOOTy: removed by ` + luksUnitName + ` after the TPM2 enrollment." "$key" >"$keyconf"
	rm -f /boot/initrd.img-*.booty-new
	say "failed at $step; the enrollment key unlocks until the next try"
	status failed "phase 1: $step"
}
trap restore EXIT
step="TPM2 enrollment"
# --wipe-slot=tpm2 replaces the TPM2 slot of an earlier failed run.
systemd-cryptenroll --unlock-key-file="$key" --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs="$TPM2_PCRS" "$dev"
step="TPM2 unlock test"
cryptsetup open --test-passphrase --token-only --token-type systemd-tpm2 "$dev"
say "the TPM2 unlocks $dev with PCR $TPM2_PCRS"
step="initramfs"
crypttab_line "none luks,x-initrd.attach,tpm2-device=auto"
rm -f "$keyconf"
built=""
for img in /boot/initrd.img-*; do
	case $img in *.booty-new | *.booty-old) continue ;; esac
	[ -e "$img" ] || continue
	kver=${img#/boot/initrd.img-}
	[ -d "/usr/lib/modules/$kver" ] || continue
	step="dracut $kver"
	dracut --force "$img.booty-new" "$kver"
	list=$(lsinitrd "$img.booty-new")
	case $list in *luks-enroll.key*) step="$kver still has the enrollment key"; exit 1 ;; esac
	for f in libtss2-esys.so libtss2-rc.so libtss2-mu.so libtss2-tcti-device.so libcryptsetup-token-systemd-tpm2.so systemd-cryptsetup etc/crypttab; do
		case $list in *"$f"*) ;; *) step="$kver lacks $f"; exit 1 ;; esac
	done
	built="$built $img"
done
step="no initramfs"
[ -n "$built" ]
step="initramfs swap"
for img in $built; do cp -p "$img" "$img.booty-old"; done
sync
for img in $built; do mv "$img.booty-new" "$img"; done
sync
: >"$bound"
trap - EXIT
rm -f /boot/initrd.img-*.booty-old
status tpm2-bound "bound to PCR $TPM2_PCRS; key slot $LUKS_ENROLL_SLOT goes after a boot that the TPM2 unlocked"
say "bound to PCR $TPM2_PCRS; key slot $LUKS_ENROLL_SLOT goes on the next boot that the TPM2 unlocks"
`
