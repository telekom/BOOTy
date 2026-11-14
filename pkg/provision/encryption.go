//go:build linux

package provision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/executil"
)

// Disk encryption (docs/disk-encryption.md). prepare-disk-encryption writes
// crypttab, the dracut configuration, a one-time enrollment key and the
// first-boot TPM2 enrollment unit into the target and rebuilds its
// initramfs. encrypt-disk, after the target is unmounted, hands a random
// recovery key to the provisioner and encrypts the partition in place:
// key slot 0 holds the recovery key, slot 1 the enrollment key. On the
// first boot with Secure Boot on, the unit binds the volume to the TPM2
// (PCR 7+14), rebuilds the initramfs without the enrollment key and wipes
// slot 1.
const (
	luksMapperName    = "booty_crypt"
	luksEnrollKeyPath = "/etc/booty/luks-enroll.key"
	luksEnvPath       = "/etc/booty/luks-tpm2.env"
	luksScriptPath    = "/usr/local/sbin/booty-luks-tpm2-enroll"
	luksUnitName      = "booty-luks-tpm2-enroll.service"
	luksDracutConf    = "/etc/dracut.conf.d/90-booty-luks.conf"
	luksDracutKeyConf = "/etc/dracut.conf.d/91-booty-luks-enroll-key.conf"
	luksGrubConf      = "/etc/default/grub.d/99-booty-luks.cfg"
	// luksTPM2PCRs: PCR 7 is the Secure Boot state and db, PCR 14 the
	// MokList that shim measures. PCR 7 alone does not see an added MOK.
	luksTPM2PCRs      = "7+14"
	luksRecoverySlot  = 0
	luksEnrollSlot    = 1
	luksHeaderReserve = 32 << 20 // --reduce-device-size, twice the LUKS2 header
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

// encryptionState is what prepare-disk-encryption hands to encrypt-disk.
type encryptionState struct {
	device    string
	luksUUID  string
	enrollKey []byte
}

func (o *Orchestrator) encryptionEnabled() bool {
	return o.cfg.Provision.DiskEncryption.Enabled()
}

// prepareDiskEncryption checks the prerequisites and writes the unlock
// configuration into the target root. With DISK_ENCRYPTION_ALLOW_FAILURE a
// failed check provisions without encryption; nothing has changed then.
func (o *Orchestrator) prepareDiskEncryption(ctx context.Context) error {
	o.encryption = nil
	if !o.encryptionEnabled() {
		return nil
	}
	device, err := o.encryptionPreflight(ctx)
	if err != nil {
		if o.cfg.Provision.DiskEncryption.AllowFailure {
			o.log.Warn("disk encryption is not possible, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)", "error", err)
			return nil
		}
		return fmt.Errorf("disk encryption preflight: %w", err)
	}
	st := &encryptionState{device: device, enrollKey: make([]byte, 64)}
	if st.luksUUID, err = randomUUID(); err != nil {
		return err
	}
	if _, err := rand.Read(st.enrollKey); err != nil {
		return fmt.Errorf("generate enrollment key: %w", err)
	}
	if err := writeEncryptionFiles(newroot, st, luksTargetLibs(newroot)); err != nil {
		clear(st.enrollKey)
		return err
	}
	if out, err := o.disk.ChrootRun(ctx, newroot, "dracut --force --regenerate-all"); err != nil {
		clear(st.enrollKey)
		return fmt.Errorf("rebuild initramfs with the LUKS unlock: %s: %w", strings.TrimSpace(string(out)), err)
	}
	o.encryption = st
	o.log.Info("disk encryption prepared", "device", device, "luksUUID", st.luksUUID, "tpm2PCRs", luksTPM2PCRs)
	return nil
}

// encryptionPreflight returns the partition to encrypt or why it cannot be.
func (o *Orchestrator) encryptionPreflight(ctx context.Context) (string, error) {
	num, err := o.cfg.Provision.DiskEncryption.TargetPartition()
	if err != nil {
		return "", err
	}
	device := o.rootPartition
	if num > 0 {
		device = disk.PartitionDevicePath(o.targetDisk, num)
	}
	if _, err := disk.PartitionNumberChecked(device, o.targetDisk); err != nil {
		return "", fmt.Errorf("%s is not a partition of the target disk %s: %w", device, o.targetDisk, err)
	}
	if num == 0 || device == o.rootPartition {
		if !o.separateBootMounted {
			return "", errors.New("the root needs a separate /boot partition: GRUB cannot read the kernel from an encrypted root")
		}
	}
	for _, mnt := range []string{separateBootMountPoint(), bootEFIMountPoint()} {
		if src, ok := mountedSource(mnt); ok && sameMountSource(src, device) {
			return "", fmt.Errorf("%s is mounted at %s; the boot partitions stay unencrypted", device, mnt)
		}
	}
	fs, err := o.disk.FilesystemType(ctx, device)
	if err != nil {
		return "", fmt.Errorf("file system of %s: %w", device, err)
	}
	if fs != "ext4" {
		return "", fmt.Errorf("%s has file system %q; in-place encryption needs ext4 to shrink it for the LUKS2 header", device, fs)
	}
	if _, err := os.Stat("/sys/class/tpm/tpm0"); err != nil {
		return "", fmt.Errorf("no TPM: %w", err)
	}
	for _, tool := range []string{"cryptsetup", "e2fsck", "resize2fs"} {
		if _, err := executil.LookPath(tool); err != nil {
			return "", fmt.Errorf("ramdisk: %w", err)
		}
	}
	return device, checkEncryptionTarget(newroot)
}

// checkEncryptionTarget checks that the image can unlock the volume with
// dracut and systemd-cryptsetup and bind it with systemd-cryptenroll.
func checkEncryptionTarget(root string) error {
	var missing []string
	for _, rel := range luksTargetTools {
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

func writeEncryptionFiles(root string, st *encryptionState, libs []string) error {
	crypttab, err := luksCrypttab(root, st.luksUUID)
	if err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{luksEnrollKeyPath, st.enrollKey, 0o400},
		{"/etc/crypttab", crypttab, 0o644},
		{luksEnvPath, []byte(fmt.Sprintf("LUKS_UUID=%s\nLUKS_NAME=%s\nLUKS_ENROLL_SLOT=%d\nTPM2_PCRS=%s\n",
			st.luksUUID, luksMapperName, luksEnrollSlot, luksTPM2PCRs)), 0o644},
		// A host-only build adds tpm2-tss for tpm2-device= in crypttab,
		// which needs tpm2-tools, and then drops systemd-cryptsetup. The
		// generic build (the Ubuntu default) with the libraries listed here
		// does not.
		{luksDracutConf, []byte("# Written by BOOTy: unlock the LUKS2 volume in the initramfs (docs/disk-encryption.md).\n" +
			"hostonly=\"no\"\n" +
			"add_dracutmodules+=\" crypt systemd-cryptsetup \"\n" +
			"install_items+=\" /etc/crypttab " + strings.Join(libs, " ") + " \"\n"), 0o644},
		{luksDracutKeyConf, []byte("# Written by BOOTy: removed by " + luksUnitName + " after the TPM2 enrollment.\n" +
			"install_items+=\" " + luksEnrollKeyPath + " \"\n"), 0o644},
		// Ubuntu cloud images boot without initramfs by the root PARTUUID
		// first, which cannot open the volume.
		{luksGrubConf, []byte("# Written by BOOTy: the root needs the initramfs to unlock LUKS.\nGRUB_FORCE_PARTUUID=\n"), 0o644},
		{luksScriptPath, []byte(luksEnrollScript), 0o755},
		{"/etc/systemd/system/" + luksUnitName, []byte(luksEnrollUnit), 0o644},
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

// encryptDisk encrypts the prepared partition in place. The recovery key
// reaches the provisioner before the disk changes, so a failure later
// never leaves a volume whose key nobody has.
func (o *Orchestrator) encryptDisk(ctx context.Context) error {
	st := o.encryption
	if st == nil {
		return nil
	}
	defer clear(st.enrollKey)
	if isMountPoint(newroot) {
		return errors.New("the target root is still mounted")
	}
	if err := runCryptsetup(ctx, nil, nil, "isLuks", st.device); err == nil {
		return fmt.Errorf("%s is already LUKS; reprovision to encrypt it again", st.device)
	}
	recovery, err := newRecoveryKey()
	if err != nil {
		return err
	}
	defer clear(recovery)
	if err := o.escrowRecoveryKey(ctx, st, recovery); err != nil {
		return err
	}
	if err := o.shrinkForLUKSHeader(ctx, st.device); err != nil {
		return err
	}
	o.log.Info("encrypting partition in place, this rewrites the whole partition", "device", st.device, "luksUUID", st.luksUUID)
	if err := runCryptsetup(ctx, recovery, nil, "reencrypt", "--encrypt", "--type", "luks2",
		"--reduce-device-size", strconv.Itoa(luksHeaderReserve>>20)+"M", "--uuid", st.luksUUID,
		"--key-slot", strconv.Itoa(luksRecoverySlot), "--key-file", "-", "--batch-mode",
		// Both keys are random 256 bit or more; a slow KDF adds nothing.
		"--pbkdf", "pbkdf2", "--pbkdf-force-iterations", "1000", st.device); err != nil {
		return err
	}
	if err := addEnrollKey(ctx, st, recovery); err != nil {
		return err
	}
	if err := runCryptsetup(ctx, recovery, nil, "open", "--test-passphrase", "--key-slot", strconv.Itoa(luksRecoverySlot),
		"--key-file", "-", st.device); err != nil {
		return fmt.Errorf("the recovery key does not open %s: %w", st.device, err)
	}
	o.log.Info("partition encrypted", "device", st.device, "luksUUID", st.luksUUID,
		"recoverySlot", luksRecoverySlot, "enrollSlot", luksEnrollSlot, "tpm2PCRs", luksTPM2PCRs)
	return nil
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

// shrinkForLUKSHeader shrinks the ext4 file system so that reencrypt can
// move it behind the LUKS2 header.
func (o *Orchestrator) shrinkForLUKSHeader(ctx context.Context, device string) error {
	raw, err := os.ReadFile(filepath.Join(sysBlockRoot, filepath.Base(device), "size"))
	if err != nil {
		return fmt.Errorf("read size of %s: %w", device, err)
	}
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return fmt.Errorf("parse size of %s: %w", device, err)
	}
	newKiB := (sectors*512 - luksHeaderReserve) / 1024
	if newKiB <= 0 {
		return fmt.Errorf("%s is too small for a LUKS2 header", device)
	}
	if err := o.disk.CheckExtFilesystemPreen(ctx, device); err != nil {
		return err
	}
	if out, err := o.disk.Run(ctx, "resize2fs", device, strconv.FormatInt(newKiB, 10)+"K"); err != nil {
		return fmt.Errorf("shrink %s for the LUKS2 header: %s: %w", device, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// addEnrollKey adds the enrollment key to slot 1. The key goes through a
// memfd, so it never has a path on a file system of the ramdisk.
func addEnrollKey(ctx context.Context, st *encryptionState, recovery []byte) error {
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
	return runCryptsetup(ctx, recovery, f, "luksAddKey", "--key-file", "-", "--key-slot", strconv.Itoa(luksEnrollSlot),
		"--pbkdf", "pbkdf2", "--pbkdf-force-iterations", "1000", "--batch-mode", st.device, "/proc/self/fd/3")
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

// luksEnrollUnit runs the enrollment once Secure Boot is on, so PCR 7 and
// PCR 14 have their final values (also after a MOK enrollment boot).
const luksEnrollUnit = `# Written by BOOTy (docs/disk-encryption.md).
[Unit]
Description=Bind the BOOTy LUKS2 volume to the TPM2
ConditionSecurity=uefi-secureboot
ConditionPathExists=` + luksEnrollKeyPath + `
After=local-fs.target cryptsetup.target

[Service]
Type=oneshot
ExecStart=` + luksScriptPath + `

[Install]
WantedBy=multi-user.target
`

// luksEnrollScript binds the volume to the TPM2, proves that the TPM2
// unlocks it, rebuilds every initramfs without the enrollment key and
// checks it, and only then wipes the enrollment key slot and the key. Any
// failure before the wipe restores crypttab and leaves the key, so the next
// boot unlocks as before and tries again.
const luksEnrollScript = `#!/bin/sh
# Written by BOOTy (docs/disk-encryption.md).
set -eu
. ` + luksEnvPath + `
dev=/dev/disk/by-uuid/$LUKS_UUID
key=` + luksEnrollKeyPath + `
keyconf=` + luksDracutKeyConf + `
say() { echo "booty-luks-tpm2-enroll: $*"; }
restore() {
	cp /etc/booty/crypttab.enroll /etc/crypttab
	printf '# Written by BOOTy: removed by ` + luksUnitName + ` after the TPM2 enrollment.\ninstall_items+=" %s "\n' "$key" >"$keyconf"
	rm -f /boot/initrd.img-*.booty-new
	say "failed, the enrollment key stays until the next boot: $*"
	exit 1
}
# --wipe-slot=tpm2 replaces the TPM2 slot of an earlier failed run.
systemd-cryptenroll --unlock-key-file="$key" --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs="$TPM2_PCRS" "$dev"
cryptsetup open --test-passphrase --token-only --token-type systemd-tpm2 "$dev"
say "the TPM2 unlocks $dev with PCR $TPM2_PCRS"
cp /etc/crypttab /etc/booty/crypttab.enroll
sed -i "s|^$LUKS_NAME .*|$LUKS_NAME UUID=$LUKS_UUID none luks,x-initrd.attach,tpm2-device=auto|" /etc/crypttab
rm -f "$keyconf"
built=""
for img in /boot/initrd.img-*; do
	case $img in *.booty-new) continue ;; esac
	[ -e "$img" ] || continue
	kver=${img#/boot/initrd.img-}
	[ -d "/usr/lib/modules/$kver" ] || continue
	dracut --force "$img.booty-new" "$kver" || restore "dracut $kver"
	list=$(lsinitrd "$img.booty-new") || restore "lsinitrd $kver"
	case $list in *luks-enroll.key*) restore "$kver still has the enrollment key" ;; esac
	for f in libtss2-esys.so libtss2-rc.so libtss2-mu.so libtss2-tcti-device.so libcryptsetup-token-systemd-tpm2.so systemd-cryptsetup etc/crypttab; do
		case $list in *"$f"*) ;; *) restore "$kver lacks $f" ;; esac
	done
	built="$built $img"
done
[ -n "$built" ] || restore "no initramfs"
for img in $built; do mv "$img.booty-new" "$img"; done
sync
cryptsetup open --test-passphrase --key-slot "$LUKS_ENROLL_SLOT" --key-file "$key" "$dev"
cryptsetup -q luksKillSlot "$dev" "$LUKS_ENROLL_SLOT"
shred -u "$key"
rm -f /etc/booty/crypttab.enroll
systemctl disable ` + luksUnitName + `
say "done: key slot $LUKS_ENROLL_SLOT and the enrollment key are gone, the TPM2 (PCR $TPM2_PCRS) and the recovery key unlock $dev"
`
