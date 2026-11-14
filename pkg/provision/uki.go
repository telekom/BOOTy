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
	"maps"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

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
)

// ukiPerHostRefused are UKI sections that per-host signing refuses: the
// ones that it adds, and the ones whose PCR 11 measurement
// ukiPCRSignature does not model.
var ukiPerHostRefused = []string{"pcrsig", "pcrpkey", "dtbauto", "hwids", "efifw"}

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
)

// loadUKIDigest reads the UKI digest from the manifest of the verified
// image. A missing or malformed annotation fails verify-image, before
// wipe-disks; with DISK_ENCRYPTION_ALLOW_FAILURE the encryption preflight
// in stream-image falls back to an unencrypted install instead.
func (o *Orchestrator) loadUKIDigest(ctx context.Context, imageURL string) error {
	if !o.cfg.Provision.DiskEncryption.UKI() {
		return nil
	}
	v, err := ociManifestAnnotation(ctx, imageURL, UKIDigestAnnotation)
	if err != nil {
		return fmt.Errorf("read the UKI digest annotation: %w", err)
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if _, err := hex.DecodeString(v); err != nil || len(v) != 2*sha256.Size {
		if o.cfg.Provision.DiskEncryption.AllowFailure {
			return nil
		}
		return fmt.Errorf("no UKI digest: the image manifest has no hex SHA-256 in the annotation %s", UKIDigestAnnotation)
	}
	o.ukiDigest = v
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

// installPerHostUKI signs the unsigned UKI with per-host keys and replaces
// it on the ESP, in place: the ESP may not fit two copies. u.data becomes
// the signed UKI.
func (o *Orchestrator) installPerHostUKI(esp string, st *ukiState, u *imageUKI) (pubPEM, sigJSON []byte, err error) {
	if u.signed {
		return nil, nil, errors.New("already signed; SECUREBOOT_SIGNING=per-host needs the unsigned UKI")
	}
	signed, pubPEM, sigJSON, err := o.signUKIPerHost(st, u.data, u.sections)
	if err != nil {
		return nil, nil, err
	}
	espRoot, err := os.OpenRoot(esp)
	if err != nil {
		return nil, nil, fmt.Errorf("open the ESP: %w", err)
	}
	defer espRoot.Close() //nolint:errcheck // closed after the write returned
	if err := espRoot.WriteFile(u.rel, signed, 0o644); err != nil {
		return nil, nil, fmt.Errorf("install the signed UKI: %w", err)
	}
	u.data = signed
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: st.cert})
	if err := writeTargetRootFile(esp, filepath.Join(esp, ukiCertESPPath), certPEM); err != nil {
		return nil, nil, fmt.Errorf("write the UKI signing certificate: %w", err)
	}
	return pubPEM, sigJSON, nil
}

// readImageUKI returns the one UKI below /EFI/Linux of the ESP.
func readImageUKI(esp *os.Root) (rel string, data []byte, err error) {
	entries, err := fs.ReadDir(esp.FS(), ukiESPDir)
	if err != nil {
		return "", nil, fmt.Errorf("the image has no UKI: %w", err)
	}
	var names []string
	for _, e := range entries {
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
	if f := o.cfg.Provision.DiskEncryption.PCRPublicKeyFile; f != "" {
		raw, err := os.ReadFile(f) //nolint:gosec // configured trust material in the ramdisk
		if err != nil {
			return nil, nil, fmt.Errorf("read DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE: %w", err)
		}
		pinned, err := parsePublicKeyPEM(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE: %w", err)
		}
		if !bytes.Equal(pub, pinned) {
			return nil, nil, errors.New(".pcrpkey is not the key in DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE")
		}
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
