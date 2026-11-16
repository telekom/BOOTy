//go:build linux

package provision

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"debug/pe"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/image"
)

// Signed UKI with a signed PCR 11 policy (DISK_ENCRYPTION=luks2-tpm2-uki,
// docs/disk-encryption.md#signed-uki-and-pcr-11). The image ships exactly
// one UKI at /EFI/Linux/*.efi on the ESP (UAPI Boot Loader Specification
// type #2) and names its SHA-256 in the manifest annotation
// UKIDigestAnnotation, which the cosign signature of the image covers.
//
//   - SECUREBOOT_SIGNING=per-host: the UKI is unsigned. BOOTy makes a PCR
//     policy key and a Secure Boot key for this host, adds .pcrpkey and the
//     signed expected PCR 11 values (.pcrsig), signs the UKI for Secure
//     Boot, all in Go (uki_sign.go), and wipes both private keys. The
//     certificate goes to the report and to the ESP; the provisioner
//     enrolls it in db.
//   - SECUREBOOT_SIGNING=ci: the UKI is signed and carries .pcrsig and
//     .pcrpkey from the image pipeline. BOOTy makes no key; it only
//     verifies the UKI and installs its public policy key.
//
// Either way the first boot with Secure Boot on binds the volume to PCR 7
// and to the PCR 11 policy key, and BOOTy adds a boot entry for the UKI.
const (
	// UKIDigestAnnotation is the image manifest annotation with the hex
	// SHA-256 of the UKI file as the image ships it.
	UKIDigestAnnotation = "com.github.telekom.booty.uki.sha256"
	ukiESPDir           = "EFI/Linux"
	// ukiCertESPPath holds the per-host Secure Boot certificate (PEM) on
	// the ESP for the provisioner; it is public.
	ukiCertESPPath      = "EFI/BOOTy/uki-signing.crt"
	ukiPCRPublicKeyPath = "/etc/booty/tpm2-pcr-public-key.pem"
	ukiPCRSignaturePath = "/etc/booty/tpm2-pcr-signature.json"
	ukiBootLabel        = "booty-uki"
	// ukiTPM2PCRs is bound next to the PCR 11 policy: PCR 7 is the Secure
	// Boot state and the db entry that verified the UKI. PCR 14 (MokList)
	// is not measured on the direct firmware boot of the UKI.
	ukiTPM2PCRs = "7"
	ukiKeyBits  = 2048
	maxUKISize  = 512 << 20
	// ukiTempSuffix names the temporary file of the atomic UKI install,
	// .<name>.efi.booty-tmp next to the UKI: no UKI loader picks it up.
	ukiTempSuffix = ".booty-tmp"
	// ukiESPMargin is the ESP space that the atomic install needs on top
	// of a second copy of the UKI: the sections that per-host signing
	// adds, the certificate and FAT cluster rounding.
	ukiESPMargin = 4 << 20
	// ukiMaxCmdline is the longest UKI .cmdline: COMMAND_LINE_SIZE of
	// x86-64 is 2048 bytes with the NUL.
	ukiMaxCmdline = 2047
)

// ukiDeniedParams are kernel parameters that per-host signing does not
// merge into the .cmdline of the UKI: they replace init, stop the boot in
// a shell, debug it or start other units, and the PCR 11 signature would
// bless them, or stop the TPM2 enrollment. Names compare with "-" as "_",
// as systemd reads them. Words that start with "-" (-b, and "--", after
// which the kernel passes the rest to init as arguments) are refused too.
var ukiDeniedParams = []string{
	"init", "rdinit", "single", "s", "S", "1", "emergency", "rescue",
	"rd.break", "rd.shell", "rd.emergency", "rd.rescue", "rd.debug", "rd.udev.debug", "rd.systemd.debug_shell",
	"rd.systemd.unit", "rd.systemd.run", "rd.systemd.break", "rd.systemd.wants",
	"systemd.debug_shell", "systemd.unit", "systemd.run", "systemd.break", "systemd.wants",
	"systemd.mask", "rd.systemd.mask", "systemd.set_credential", "systemd.set_credential_binary", "systemd.import_credentials",
	"systemd.volatile",
	"systemd.run_success_action", "systemd.run_failure_action", "systemd.confirm_spawn", "systemd.log_level",
	"kgdboc", "kgdbwait", "kgdbcon", "ekgdboc",
}

// ukiPerHostRefused are UKI sections that per-host signing refuses: the
// ones that it adds, and the ones whose PCR 11 measurement
// ukiPCRSignature does not model.
var ukiPerHostRefused = append([]string{"pcrsig", "pcrpkey"}, ukiUnmodeledSections...)

// ukiUnmodeledSections are measured by systemd-stub in ways that
// ukiPCRPolicies does not model.
var ukiUnmodeledSections = []string{"dtbauto", "hwids", "efifw"}

// The binaries that the installed system needs to bind the volume.
var ukiTargetTools = []string{"usr/bin/systemd-cryptenroll", "usr/sbin/cryptsetup"}

// ukiState is what prepare-disk-encryption hands to the later steps.
type ukiState struct {
	signing    string
	loader     string // firmware path, \EFI\Linux\<name>.efi
	esp, rel   string // the ESP mount point and the UKI below it
	sha256     string // of the installed UKI
	certSHA256 string // per-host Secure Boot certificate
	cert       []byte // DER
}

// ReportUKI is the installed UKI. Cert (DER, base64 in JSON) is the
// per-host Secure Boot certificate that the provisioner enrolls in db.
type ReportUKI struct {
	Signing    string `json:"signing"`
	Loader     string `json:"loader"`
	SHA256     string `json:"sha256"`
	CertSHA256 string `json:"certSHA256,omitempty"`
	Cert       []byte `json:"cert,omitempty"`
}

// uki functions and paths that tests replace.
var (
	ociManifestAnnotation = image.OCIManifestAnnotation
	// newUKISigningKey generates a per-host key. SECUREBOOT_SIGNING=ci must
	// never call it (TestPrepareUKICIMakesNoKey).
	newUKISigningKey = func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, ukiKeyBits) }
	// espFreeBytes is the space that an unprivileged writer may still use.
	espFreeBytes = func(dir string) (uint64, error) {
		var st unix.Statfs_t
		if err := unix.Statfs(dir, &st); err != nil {
			return 0, fmt.Errorf("statfs %s: %w", dir, err)
		}
		return st.Bavail * uint64(st.Bsize), nil //nolint:gosec // block size is positive
	}
	// syncUKIFile flushes the temporary UKI before the rename.
	syncUKIFile = (*os.File).Sync
)

// loadUKIDigest reads the UKI digest from the manifest of the verified
// image. A missing or malformed annotation fails verify-image, before
// wipe-disks; with DISK_ENCRYPTION_ALLOW_FAILURE the encryption preflight
// in stream-image falls back to an unencrypted install instead.
func (o *Orchestrator) loadUKIDigest(ctx context.Context, imageURL string) error {
	if !o.cfg.Provision.DiskEncryption.UKI() {
		return nil
	}
	var v string
	if imageURL != "" {
		var err error
		if v, err = ociManifestAnnotation(ctx, imageURL, UKIDigestAnnotation); err != nil {
			return fmt.Errorf("read the UKI digest annotation: %w", err)
		}
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if _, err := hex.DecodeString(v); err != nil || len(v) != 2*sha256.Size {
		if o.cfg.Provision.DiskEncryption.AllowFailure {
			return nil
		}
		return fmt.Errorf("no UKI digest: the image must be a cosign-verified oci:// image whose manifest has a hex SHA-256 in the annotation %s", UKIDigestAnnotation)
	}
	o.ukiDigest = v
	return o.loadPCRPin()
}

// loadPCRPin reads DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE before wipe-disks:
// a deploy archive can not replace the file (caprf), but its /deploy swap
// can remove it.
func (o *Orchestrator) loadPCRPin() error {
	o.ukiPCRPin = nil
	f := o.cfg.Provision.DiskEncryption.PCRPublicKeyFile
	if f == "" {
		return nil
	}
	raw, err := os.ReadFile(f) //nolint:gosec // configured trust material in the ramdisk
	if err != nil {
		return fmt.Errorf("read DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE: %w", err)
	}
	if o.ukiPCRPin, err = parsePublicKeyPEM(raw); err != nil {
		return fmt.Errorf("DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE: %w", err)
	}
	return nil
}

// imageUKI is the UKI of the image, checked against the signed digest.
type imageUKI struct {
	rel      string // below the ESP
	data     []byte
	sections map[string][]byte
	signed   bool
}

// prepareUKI verifies the UKI of the image against the signed digest,
// signs it with per-host keys if asked, and installs the public PCR policy
// key and signature for the first-boot enrollment.
func (o *Orchestrator) prepareUKI(root, esp string) (*ukiState, error) {
	signing := o.cfg.Provision.SecureBoot.SigningMode()
	u, err := o.loadImageUKI(root, esp)
	if err != nil {
		return nil, err
	}
	st := &ukiState{signing: signing, loader: `\` + strings.ReplaceAll(u.rel, "/", `\`), esp: esp, rel: u.rel}
	var pubPEM, sigJSON []byte
	switch signing {
	case config.SigningCI:
		if p, perr := o.config.KernelParams(o.cfg); perr == nil && p != "" {
			o.log.Warn("the UKI signed by the image pipeline keeps its .cmdline: the per-host kernel parameters reach the GRUB fallback entry only", "params", p)
		}
		pubPEM, sigJSON, err = o.checkCIUKI(u.sections, u.signed)
	case config.SigningPerHost:
		pubPEM, sigJSON, err = o.installPerHostUKI(esp, st, u)
	default:
		err = fmt.Errorf("SECUREBOOT_SIGNING %q cannot sign a UKI", signing)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u.rel, err)
	}
	sum := sha256.Sum256(u.data)
	st.sha256 = hex.EncodeToString(sum[:])
	for p, b := range map[string][]byte{ukiPCRPublicKeyPath: pubPEM, ukiPCRSignaturePath: sigJSON} {
		if err := writeTargetRootFile(root, filepath.Join(root, p), b); err != nil {
			return nil, fmt.Errorf("write %s: %w", p, err)
		}
	}
	o.log.Info("UKI prepared", "loader", st.loader, "signing", signing, "sha256", st.sha256, "certSHA256", st.certSHA256)
	return st, nil
}

// loadImageUKI checks the target tools and reads the UKI from the ESP; its
// SHA-256 must be the one of the signed image manifest.
func (o *Orchestrator) loadImageUKI(root, esp string) (*imageUKI, error) {
	if err := checkTargetFiles(root, ukiTargetTools, "Ubuntu: systemd-cryptsetup, libtss2-rc0t64"); err != nil {
		return nil, err
	}
	if len(o.ukiDigest) != sha256.Size*2 {
		return nil, fmt.Errorf("no UKI digest: the image must be a cosign-verified oci:// image whose manifest has the annotation %s", UKIDigestAnnotation)
	}
	if !isMountPoint(esp) {
		return nil, errors.New("the UKI needs the ESP mounted at /boot/efi")
	}
	espRoot, err := os.OpenRoot(esp)
	if err != nil {
		return nil, fmt.Errorf("open the ESP: %w", err)
	}
	defer espRoot.Close() //nolint:errcheck // read-only handle
	u := &imageUKI{}
	if u.rel, u.data, err = readImageUKI(espRoot); err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(u.data); hex.EncodeToString(sum[:]) != o.ukiDigest {
		if _, _, err := ukiSections(u.data); err != nil {
			return nil, fmt.Errorf("%s is truncated or corrupt (%d bytes; an interrupted install?), not the UKI of the image: %w", u.rel, len(u.data), err)
		}
		return nil, fmt.Errorf("%s has SHA-256 %x, the signed image manifest says %s", u.rel, sum, o.ukiDigest)
	}
	if u.sections, u.signed, err = ukiSections(u.data); err != nil {
		return nil, fmt.Errorf("%s: %w", u.rel, err)
	}
	// Without an embedded command line systemd-stub takes the one of the
	// boot entry, also with Secure Boot on, and measures it into PCR 12,
	// which the volume is not bound to.
	if len(bytes.Trim(u.sections["cmdline"], "\x00 \t\n")) == 0 {
		return nil, fmt.Errorf("%s has no .cmdline: systemd-stub would accept a command line from the boot entry", u.rel)
	}
	return u, nil
}

// installPerHostUKI signs the unsigned UKI with per-host keys, with the
// per-host kernel parameters in its .cmdline, and replaces it on the ESP.
// u.data becomes the signed UKI.
func (o *Orchestrator) installPerHostUKI(esp string, st *ukiState, u *imageUKI) (pubPEM, sigJSON []byte, err error) {
	if u.signed {
		return nil, nil, errors.New("already signed; SECUREBOOT_SIGNING=per-host needs the unsigned UKI")
	}
	inplace := o.cfg.Provision.DiskEncryption.UKIInplaceWrite
	if !inplace {
		if err := checkESPRoom(esp, len(u.data)); err != nil {
			return nil, nil, err
		}
	}
	cmdline, err := o.ukiCmdline(u.sections["cmdline"])
	if err != nil {
		return nil, nil, err
	}
	data, sections := u.data, u.sections
	if cmdline != nil {
		// Before signUKIPerHost, so the PCR 11 policy covers it.
		if data, err = replacePESection(data, ".cmdline", cmdline); err != nil {
			return nil, nil, fmt.Errorf("set the per-host .cmdline: %w", err)
		}
		sections = maps.Clone(sections)
		sections["cmdline"] = cmdline
	}
	signed, pubPEM, sigJSON, err := o.signUKIPerHost(st, data, sections)
	if err != nil {
		return nil, nil, err
	}
	espRoot, err := os.OpenRoot(esp)
	if err != nil {
		return nil, nil, fmt.Errorf("open the ESP: %w", err)
	}
	defer espRoot.Close() //nolint:errcheck // closed after the write returned
	if err := installUKIFile(espRoot, u.rel, signed, inplace); err != nil {
		return nil, nil, fmt.Errorf("install the signed UKI: %w", err)
	}
	u.data = signed
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: st.cert})
	if err := writeTargetRootFile(esp, filepath.Join(esp, ukiCertESPPath), certPEM); err != nil {
		return nil, nil, fmt.Errorf("write the UKI signing certificate: %w", err)
	}
	return pubPEM, sigJSON, nil
}

// ukiCmdline returns the .cmdline for per-host signing: the one of the
// image with the per-host kernel parameters of the GRUB drop-in appended,
// or nil to keep it (no parameters, UKI_KEEP_IMAGE_CMDLINE).
func (o *Orchestrator) ukiCmdline(imageCmdline []byte) ([]byte, error) {
	if o.cfg.Provision.DiskEncryption.UKIKeepImageCmdline {
		return nil, nil
	}
	params, err := o.config.KernelParams(o.cfg)
	if err != nil {
		return nil, fmt.Errorf("per-host kernel parameters: %w", err)
	}
	if params == "" {
		return nil, nil
	}
	if err := checkUKIKernelParams(params); err != nil {
		return nil, err
	}
	base := bytes.TrimRight(imageCmdline, "\x00 \t\n")
	cmdline := slices.Concat(base, []byte(" "+params), imageCmdline[len(base):])
	if len(bytes.TrimRight(cmdline, "\x00")) > ukiMaxCmdline {
		return nil, fmt.Errorf("the UKI .cmdline with the per-host kernel parameters is longer than %d bytes", ukiMaxCmdline)
	}
	o.log.Info("per-host kernel parameters go into the UKI .cmdline", "params", params)
	return cmdline, nil
}

// checkUKIKernelParams refuses parameters that are not printable ASCII or
// in ukiDeniedParams.
func checkUKIKernelParams(params string) error {
	for _, c := range []byte(params) {
		if c < ' ' || c > '~' {
			return fmt.Errorf("per-host kernel parameters for the UKI must be printable ASCII: %q", params)
		}
	}
	// The kernel and systemd strip quotes, so a quoted name could pass the
	// deny list.
	if strings.ContainsAny(params, "\"'\\") {
		return fmt.Errorf("per-host kernel parameters for the UKI must not contain quotes or backslashes: %q", params)
	}
	norm := func(s string) string { return strings.ReplaceAll(s, "-", "_") }
	for p := range strings.FieldsSeq(params) {
		name, _, _ := strings.Cut(p, "=")
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("per-host kernel parameter %q is not allowed in the UKI .cmdline: words that start with - go to init (docs/disk-encryption.md#per-host-kernel-parameters)", name)
		}
		if slices.ContainsFunc(ukiDeniedParams, func(d string) bool { return norm(d) == norm(name) }) {
			return fmt.Errorf("per-host kernel parameter %q is not allowed in the UKI .cmdline: it debugs the boot or opens a shell (docs/disk-encryption.md#per-host-kernel-parameters)", name)
		}
	}
	return nil
}

// checkESPRoom checks that the ESP has room for a second copy of the UKI
// of size bytes, which the atomic install writes before the rename.
func checkESPRoom(esp string, size int) error {
	free, err := espFreeBytes(esp)
	if err != nil {
		return fmt.Errorf("free space of the ESP: %w", err)
	}
	need := uint64(size) + ukiESPMargin //nolint:gosec // a file size
	if free < need {
		return fmt.Errorf("the ESP has %d MiB free, installing the signed UKI atomically needs %d MiB (a second copy of the %d MiB UKI and %d MiB margin): "+
			"make the ESP at least twice the UKI size plus margin, or set UKI_INPLACE_WRITE=true", free>>20, (need+1<<20-1)>>20, (size+1<<20-1)>>20, ukiESPMargin>>20)
	}
	return nil
}

// ukiTempPath is the temporary file of the atomic install of rel.
func ukiTempPath(rel string) string {
	return path.Join(path.Dir(rel), "."+path.Base(rel)+ukiTempSuffix)
}

// installUKIFile replaces rel below esp with data: written to a temporary
// file in the same directory, synced, renamed over rel, and the directory
// synced, so an interruption leaves the old or the new UKI. FAT has no
// journal: a power loss during the rename can still need fsck.fat, which
// systemd-fsck runs at boot. inplace truncates and rewrites rel instead.
func installUKIFile(esp *os.Root, rel string, data []byte, inplace bool) (err error) {
	if inplace {
		return esp.WriteFile(rel, data, 0o644) //nolint:wrapcheck // wrapped by the caller
	}
	tmp := ukiTempPath(rel)
	if err := esp.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", tmp, err)
	}
	f, err := esp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = esp.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err = syncUKIFile(f); err != nil {
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err = esp.Rename(tmp, rel); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, rel, err)
	}
	d, err := esp.Open(path.Dir(rel))
	if err != nil {
		return fmt.Errorf("open %s: %w", path.Dir(rel), err)
	}
	defer d.Close() //nolint:errcheck // synced below
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path.Dir(rel), err)
	}
	return nil
}

// readImageUKI returns the one UKI below /EFI/Linux of the ESP. It removes
// a temporary file that an interrupted atomic install left.
func readImageUKI(esp *os.Root) (rel string, data []byte, err error) {
	entries, err := fs.ReadDir(esp.FS(), ukiESPDir)
	if err != nil {
		return "", nil, fmt.Errorf("the image has no UKI: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ukiTempSuffix) {
			if err := esp.Remove(path.Join(ukiESPDir, e.Name())); err != nil {
				return "", nil, fmt.Errorf("remove the leftover %s: %w", e.Name(), err)
			}
			slog.Info("removed the temporary file of an interrupted UKI install", "file", path.Join(ukiESPDir, e.Name()))
			continue
		}
		if e.Type().IsRegular() && strings.EqualFold(path.Ext(e.Name()), ".efi") {
			names = append(names, path.Join(ukiESPDir, e.Name()))
		}
	}
	if len(names) != 1 {
		return "", nil, fmt.Errorf("want exactly one UKI in /%s of the ESP, found %v", ukiESPDir, names)
	}
	if fi, err := esp.Stat(names[0]); err != nil || fi.Size() > maxUKISize {
		return "", nil, fmt.Errorf("%s: unreadable or larger than %d MiB: %w", names[0], maxUKISize>>20, err)
	}
	data, err = esp.ReadFile(names[0])
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", names[0], err)
	}
	return names[0], data, nil
}

// ukiSections returns the UKI sections by name without the dot, cut to
// their virtual size as systemd-stub measures them, and whether the PE
// carries an Authenticode signature.
func ukiSections(data []byte) (secs map[string][]byte, signed bool, err error) {
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("not a PE file: %w", err)
	}
	defer f.Close() //nolint:errcheck // reader-backed
	secs = map[string][]byte{}
	for _, s := range f.Sections {
		b, err := s.Data()
		if err != nil {
			return nil, false, fmt.Errorf("section %s: %w", s.Name, err)
		}
		if int(s.VirtualSize) < len(b) {
			b = b[:s.VirtualSize]
		}
		secs[strings.TrimPrefix(s.Name, ".")] = b
	}
	if secs["linux"] == nil {
		return nil, false, errors.New("not a UKI: no .linux section")
	}
	if secs["profile"] != nil {
		return nil, false, errors.New("multi-profile UKIs are not supported")
	}
	var security pe.DataDirectory
	if oh, ok := f.OptionalHeader.(*pe.OptionalHeader64); ok && len(oh.DataDirectory) > pe.IMAGE_DIRECTORY_ENTRY_SECURITY {
		security = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
	}
	return secs, security.Size > 0, nil
}

// checkCIUKI checks the UKI signed by the image pipeline and returns its
// PCR public key and signature.
func (o *Orchestrator) checkCIUKI(sections map[string][]byte, signed bool) (pubPEM, sigJSON []byte, err error) {
	pubPEM, sigJSON = sections["pcrpkey"], sections["pcrsig"]
	switch {
	case !signed:
		return nil, nil, errors.New("SECUREBOOT_SIGNING=ci needs a UKI signed for Secure Boot")
	case pubPEM == nil || sigJSON == nil:
		return nil, nil, errors.New("SECUREBOOT_SIGNING=ci needs the .pcrpkey and .pcrsig sections")
	}
	pub, err := parsePublicKeyPEM(pubPEM)
	if err != nil {
		return nil, nil, fmt.Errorf(".pcrpkey: %w", err)
	}
	if o.cfg.Provision.DiskEncryption.PCRPublicKeyFile != "" && !bytes.Equal(pub, o.ukiPCRPin) {
		return nil, nil, errors.New(".pcrpkey is not the key in DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE")
	}
	if err := checkUKIInitrdPolicy(sections, sigJSON, pub); err != nil {
		return nil, nil, err
	}
	return pubPEM, sigJSON, nil
}

// parsePublicKeyPEM returns the DER of the one PUBLIC KEY block in data.
func parsePublicKeyPEM(data []byte) ([]byte, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("no PEM PUBLIC KEY block")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return block.Bytes, nil
}

// signUKIPerHost adds the .pcrpkey and .pcrsig of two new keys to the
// unsigned UKI, signs it for Secure Boot and wipes the keys. It runs no
// program: the private keys never leave BOOTy (uki_sign.go).
func (o *Orchestrator) signUKIPerHost(st *ukiState, data []byte, sections map[string][]byte) (signed, pubPEM, sigJSON []byte, err error) {
	for _, name := range ukiPerHostRefused {
		if sections[name] != nil {
			return nil, nil, nil, fmt.Errorf("SECUREBOOT_SIGNING=per-host cannot sign a UKI with a .%s section", name)
		}
	}
	pcrKey, sbKey, pubPEM, err := o.newPerHostKeys(st)
	if err != nil {
		return nil, nil, nil, err
	}
	defer destroyRSAKey(pcrKey)
	defer destroyRSAKey(sbKey)
	measured := maps.Clone(sections)
	measured["pcrpkey"] = pubPEM
	if sigJSON, err = ukiPCRSignature(measured, pcrKey); err != nil {
		return nil, nil, nil, err
	}
	unsigned, err := appendPESections(data, [2][]byte{[]byte(".pcrpkey"), pubPEM}, [2][]byte{[]byte(".pcrsig"), sigJSON})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("add .pcrpkey and .pcrsig: %w", err)
	}
	cert, err := x509.ParseCertificate(st.cert)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("the UKI signing certificate: %w", err)
	}
	if signed, err = authenticodeSign(unsigned, sbKey, cert); err != nil {
		return nil, nil, nil, err
	}
	secs, ok, err := ukiSections(signed)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("the signed UKI: %w", err)
	}
	for name, b := range sections {
		if !bytes.Equal(secs[name], b) {
			return nil, nil, nil, fmt.Errorf("the signed UKI changed the .%s section", name)
		}
	}
	if !ok || !bytes.Equal(secs["pcrsig"], sigJSON) || !bytes.Equal(secs["pcrpkey"], pubPEM) {
		return nil, nil, nil, errors.New("the signed UKI lacks the Secure Boot signature, the .pcrsig or the .pcrpkey of this run")
	}
	o.log.Info("UKI signed with per-host keys; the private keys are wiped", "certSHA256", st.certSHA256)
	return signed, pubPEM, sigJSON, nil
}

// newPerHostKeys makes the PCR policy key and the Secure Boot key with its
// certificate (in st). The caller destroys the keys.
func (o *Orchestrator) newPerHostKeys(st *ukiState) (pcrKey, sbKey *rsa.PrivateKey, pubPEM []byte, err error) {
	if pcrKey, err = newUKISigningKey(); err != nil {
		return nil, nil, nil, fmt.Errorf("generate the PCR policy key: %w", err)
	}
	if sbKey, err = newUKISigningKey(); err == nil {
		st.cert, err = ukiCertificate(sbKey, "BOOTy UKI signing "+o.cfg.Hostname)
	}
	var pubDER []byte
	if err == nil {
		pubDER, err = x509.MarshalPKIXPublicKey(&pcrKey.PublicKey)
	}
	if err != nil {
		destroyRSAKey(pcrKey)
		if sbKey != nil {
			destroyRSAKey(sbKey)
		}
		return nil, nil, nil, fmt.Errorf("per-host UKI keys: %w", err)
	}
	sum := sha256.Sum256(st.cert)
	st.certSHA256 = hex.EncodeToString(sum[:])
	return pcrKey, sbKey, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), nil
}

func ukiCertificate(key *rsa.PrivateKey, cn string) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("certificate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn},
		// Firmware does not check the validity period; a wide one avoids
		// trouble with a wrong RTC.
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.AddDate(30, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create the UKI signing certificate: %w", err)
	}
	return der, nil
}

// destroyRSAKey zeroes the private parts of key (best effort: math/big may
// have left copies in freed memory; the host reboots after provisioning).
func destroyRSAKey(key *rsa.PrivateKey) {
	wipe := func(n *big.Int) {
		if n != nil {
			clear(n.Bits())
			n.SetInt64(0)
		}
	}
	wipe(key.D)
	for _, p := range key.Primes {
		wipe(p)
	}
	wipe(key.Precomputed.Dp)
	wipe(key.Precomputed.Dq)
	wipe(key.Precomputed.Qinv)
}

// createUKIBootEntry adds a firmware boot entry for the UKI. efibootmgr -c
// puts it first in BootOrder, before the GRUB entry of create-efi-boot-entry.
func (o *Orchestrator) createUKIBootEntry(ctx context.Context) error {
	if o.encryption == nil || o.encryption.uki == nil {
		return nil
	}
	if err := o.encryption.uki.checkInstalled(); err != nil {
		return err
	}
	if out, err := o.disk.Run(ctx, "efibootmgr"); err == nil {
		for line := range strings.Lines(string(out)) {
			if len(line) > 8 && strings.HasPrefix(line, "Boot") && efiBootEntryLineLabel(line) == ukiBootLabel {
				if out, err := o.disk.Run(ctx, "efibootmgr", "-b", line[4:8], "-B"); err != nil {
					return fmt.Errorf("efibootmgr remove Boot%s: %s: %w", line[4:8], strings.TrimSpace(string(out)), err)
				}
			}
		}
	}
	out, err := o.disk.Run(ctx, "efibootmgr", "-c", "-d", o.targetDisk, "-p", partNumberFromDevice(o.bootPartition),
		"-L", ukiBootLabel, "-l", o.encryption.uki.loader)
	if err != nil {
		return fmt.Errorf("efibootmgr create %s: %s: %w", ukiBootLabel, strings.TrimSpace(string(out)), err)
	}
	o.log.Info("UKI boot entry created", "loader", o.encryption.uki.loader)
	return nil
}

// checkInstalled checks that the UKI on the ESP is still the one that
// prepare-disk-encryption checked and signed: image code ran in the chroot
// since (sign-kernel-modules, post-provision commands).
func (u *ukiState) checkInstalled() error {
	espRoot, err := os.OpenRoot(u.esp)
	if err != nil {
		return fmt.Errorf("open the ESP: %w", err)
	}
	defer espRoot.Close() //nolint:errcheck // read-only handle
	data, err := espRoot.ReadFile(u.rel)
	if err != nil {
		return fmt.Errorf("read %s: %w", u.rel, err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != u.sha256 {
		return fmt.Errorf("%s changed after prepare-disk-encryption: SHA-256 %x, want %s", u.rel, sum, u.sha256)
	}
	return nil
}

// reportUKI is the UKI part of the provisioning report.
func (o *Orchestrator) reportUKI() *ReportUKI {
	if o.encryption == nil || o.encryption.uki == nil {
		return nil
	}
	u := o.encryption.uki
	return &ReportUKI{Signing: u.signing, Loader: u.loader, SHA256: u.sha256, CertSHA256: u.certSHA256, Cert: u.cert}
}
