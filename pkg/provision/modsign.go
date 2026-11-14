//go:build linux

package provision

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/efi"
	"github.com/telekom/BOOTy/pkg/secureboot"
)

const (
	// moduleCertFile is the certificate name in the module signing cert dir.
	moduleCertFile = "booty-module-signing.der"
	// mokHelperPath is mok-enroll.efi in the BOOTy ramdisk.
	mokHelperPath = "/usr/lib/booty/mok-enroll.efi"
	// mokHelperESPPath is the helper on the ESP, as a firmware path.
	mokHelperESPPath = `\EFI\BOOTy\mok-enroll.efi`
	// mokEnrollLabel is the label of the one-shot helper boot entry.
	mokEnrollLabel = "booty-mok-enroll"
)

// initramfsUpdateCmd rebuilds every initramfs of the target with
// update-initramfs, or dracut where that is missing.
const initramfsUpdateCmd = "if command -v update-initramfs >/dev/null 2>&1; then update-initramfs -u -k all; " +
	"else dracut --force --regenerate-all; fi"

var kernelReleasePattern = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

// maxCertSize caps a certificate read from the image; X.509 certificates
// are a few KiB.
const maxCertSize = 64 << 10

// readSmallRegularFile reads name in root if it is a regular file of at
// most limit bytes: the image is only semi-trusted, and a link to a device
// (the chroot binds /dev) or a huge file must not exhaust PID 1.
func readSmallRegularFile(root *os.Root, name string, limit int64) ([]byte, error) {
	// O_NONBLOCK: opening a FIFO without a writer must not block.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the file
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the file
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("not a regular file of at most %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("larger than %d bytes", limit)
	}
	return data, err
}

// onChrootBind reports whether file is on a file system that the chroot
// binds from the ramdisk (/run, /proc, /sys, /dev), through the configured
// path or a link in the image: it would vanish at teardown.
func onChrootBind(root, file string) bool {
	dev := func(p string) (uint64, bool) {
		var st syscall.Stat_t
		if syscall.Stat(p, &st) != nil {
			return 0, false
		}
		return uint64(st.Dev), true //nolint:unconvert // Dev is uint32 on some architectures
	}
	fileDev, ok := dev(file)
	rootDev, ok2 := dev(root)
	if !ok || !ok2 || fileDev == rootDev {
		return false
	}
	for _, d := range []string{"run", "proc", "sys", "dev"} {
		if bindDev, ok := dev(filepath.Join(root, d)); ok && bindDev != rootDev && bindDev == fileDev {
			return true
		}
	}
	return false
}

// ReportModuleSigning is the module signing result. Cert (DER, base64 in
// JSON) lets the provisioner enroll the certificate itself, for example into
// db over Redfish (enrollment external). Modules counts the modules that
// BOOTy signed: none with signing ci.
type ReportModuleSigning struct {
	Signing    string `json:"signing"`
	Modules    int    `json:"modules"`
	Enrollment string `json:"enrollment"`
	CertPath   string `json:"certPath"`
	CertSHA256 string `json:"certSHA256"`
	Cert       []byte `json:"cert"`
}

// moduleSigningResult applies AllowFailure: fail closed by default.
func (o *Orchestrator) moduleSigningResult(action string, err error) error {
	if err == nil {
		return nil
	}
	if o.cfg.Provision.SecureBoot.ModuleSigning.AllowFailure {
		o.log.Warn(action+" failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true", "error", err)
		return nil
	}
	return err
}

// signKernelModules prepares the certificate that enroll-module-signing-key
// enrolls. With signing per-host it signs the out-of-tree modules of the
// target with a new per-host key, installs the certificate, and rebuilds the
// module index and initramfs; the private key exists only in memory during
// this step. With signing ci it only reads the CI certificate from the
// target. With signing off (the default) it does nothing.
func (o *Orchestrator) signKernelModules(ctx context.Context) error {
	sb := &o.cfg.Provision.SecureBoot
	// A rescue retry reuses the orchestrator: never keep the certificate of
	// an earlier attempt.
	o.moduleSigning = nil
	switch sb.SigningMode() {
	case config.SigningPerHost:
		return o.moduleSigningResult("sign kernel modules", o.doSignKernelModules(ctx, sb.ModuleSigning))
	case config.SigningCI:
		return o.moduleSigningResult("read the CI module signing certificate", o.readCISigningCert(sb))
	}
	return nil
}

// readCISigningCert reads MOKCertPath from the target: the certificate of
// the CI key that signed the modules of the image.
func (o *Orchestrator) readCISigningCert(sb *config.SecureBootConfig) error {
	root, err := os.OpenRoot(o.config.rootDir)
	if err != nil {
		return fmt.Errorf("open target root: %w", err)
	}
	defer func() { _ = root.Close() }()
	der, err := readSmallRegularFile(root, strings.TrimPrefix(sb.MOKCertPath, "/"), maxCertSize)
	if err != nil {
		return fmt.Errorf("read CI module signing certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("CI module signing certificate %s is not a DER X.509 certificate: %w", sb.MOKCertPath, err)
	}
	// The image chooses this certificate and BOOTy enrolls it unattended.
	// shim trusts MokList for boot images too, except certificates with the
	// module-signing-only EKU: without it the CI key could sign boot loaders
	// and kernels that every host with the image boots.
	if !secureboot.IsModuleSigningOnly(cert) {
		return fmt.Errorf("CI module signing certificate %s lacks the module-signing-only extended key usage %s",
			sb.MOKCertPath, secureboot.OIDModuleSigningOnly)
	}
	sum := sha256.Sum256(der)
	o.moduleSigning = &ReportModuleSigning{
		Signing: config.SigningCI, Enrollment: sb.ModuleSigning.EnrollmentMethod(),
		CertPath: sb.MOKCertPath, CertSHA256: hex.EncodeToString(sum[:]), Cert: der,
	}
	o.log.Info("CI module signing certificate to enroll", "cert", sb.MOKCertPath, "certSHA256", o.moduleSigning.CertSHA256)
	return nil
}

func (o *Orchestrator) doSignKernelModules(ctx context.Context, ms config.ModuleSigningConfig) error {
	root := o.config.rootDir
	res, err := secureboot.SignModuleTree(root, "BOOTy module signing "+o.cfg.Hostname)
	if err != nil {
		return err
	}
	certPath := path.Join(ms.CertDirOrDefault(), moduleCertFile)
	if err := writeTargetRootFile(root, filepath.Join(root, certPath), res.CertDER); err != nil {
		return fmt.Errorf("install module signing certificate: %w", err)
	}
	if onChrootBind(root, filepath.Join(root, certPath)) {
		return fmt.Errorf("install module signing certificate: %s resolves to a ramdisk file system bound into the chroot", certPath)
	}
	sum := sha256.Sum256(res.CertDER)
	o.moduleSigning = &ReportModuleSigning{
		Signing: config.SigningPerHost, Modules: len(res.Modules), Enrollment: ms.EnrollmentMethod(),
		CertPath: certPath, CertSHA256: hex.EncodeToString(sum[:]), Cert: res.CertDER,
	}
	o.log.Info("signed out-of-tree kernel modules", "modules", len(res.Modules),
		"cert", certPath, "certSHA256", o.moduleSigning.CertSHA256)
	if len(res.Modules) == 0 {
		return nil
	}
	for _, k := range res.Kernels {
		if !kernelReleasePattern.MatchString(k) {
			return fmt.Errorf("unexpected kernel directory %q", k)
		}
		if out, err := o.disk.ChrootRun(ctx, root, "depmod -a "+k); err != nil {
			return fmt.Errorf("depmod %s: %s: %w", k, strings.TrimSpace(string(out)), err)
		}
	}
	if out, err := o.disk.ChrootRun(ctx, root, initramfsUpdateCmd); err != nil {
		return fmt.Errorf("rebuild initramfs: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// enrollModuleSigningKey makes the firmware trust the certificate that
// sign-kernel-modules prepared in this run. With signing per-host it never
// enrolls a certificate read from the image: with MODULE_SIGNING_ALLOW_FAILURE
// a failed signing leaves whatever the image ships at the certificate path,
// for a key its author holds. sign-kernel-modules runs again on a resume for
// the same reason. With signing ci the certificate is MOKCertPath of the
// image by design.
func (o *Orchestrator) enrollModuleSigningKey(ctx context.Context) error {
	sb := &o.cfg.Provision.SecureBoot
	if sb.SigningMode() == config.SigningOff {
		return nil
	}
	return o.moduleSigningResult("enroll module signing key", o.doEnrollModuleSigningKey(ctx, sb.ModuleSigning))
}

func (o *Orchestrator) doEnrollModuleSigningKey(ctx context.Context, ms config.ModuleSigningConfig) error {
	if o.moduleSigning == nil {
		return errors.New("no module signing certificate from this run to enroll")
	}
	cert := o.moduleSigning.Cert
	switch ms.EnrollmentMethod() {
	case config.ModuleSigningEnrollExternal:
		o.log.Info("module signing certificate left for the provisioner to enroll", "cert", o.moduleSigning.CertPath)
		return nil
	case config.ModuleSigningEnrollMokutil:
		if ok, reason := efiRuntimeReady(); !ok {
			return fmt.Errorf("mokutil enrollment: %s: %w", reason, ErrEFIVarsUnavailable)
		}
		if err := secureboot.QueueMOKEnrollment(efi.NewEFIVarReader(reportEFIVarsDir), cert, o.cfg.Provision.SecureBoot.MOKPassword); err != nil {
			return err //nolint:wrapcheck // names the variables
		}
		o.log.Info("MOK enrollment queued: MokManager asks for MOK_PASSWORD at the next boot")
		return nil
	}
	return o.stageMOKHelper(ctx, cert)
}

// stageMOKHelper copies mok-enroll.efi to the ESP and points BootNext at a
// boot entry for it that carries the certificate as optional data. The next
// firmware boot runs the helper, which writes MokList and resets.
func (o *Orchestrator) stageMOKHelper(ctx context.Context, cert []byte) error {
	if strings.TrimSpace(o.bootPartition) == "" || !isMountPoint(bootEFIMountPoint()) {
		return errors.New("efi-helper enrollment needs a mounted ESP")
	}
	if ok, reason := efiRuntimeReady(); !ok {
		return fmt.Errorf("efi-helper enrollment: %s: %w", reason, ErrEFIVarsUnavailable)
	}
	enabled, err := o.secureBootEnabled()
	if err != nil {
		return fmt.Errorf("efi-helper enrollment: read the Secure Boot state: %w", err)
	}
	if enabled {
		return errors.New("efi-helper enrollment needs Secure Boot off during provisioning: the firmware refuses the unsigned mok-enroll.efi")
	}
	helper, err := os.ReadFile(mokHelperPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", mokHelperPath, err)
	}
	esp := bootEFIMountPoint()
	dst := filepath.Join(esp, filepath.FromSlash(strings.ReplaceAll(strings.TrimPrefix(mokHelperESPPath, `\`), `\`, "/")))
	if err := writeTargetRootFile(esp, dst, helper); err != nil {
		return fmt.Errorf("install mok-enroll.efi: %w", err)
	}
	payload, err := os.CreateTemp("", "mok-esl")
	if err != nil {
		return fmt.Errorf("mok payload: %w", err)
	}
	defer func() { _ = os.Remove(payload.Name()) }()
	_, err = payload.Write(secureboot.MOKSignatureList(cert))
	if cerr := payload.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write mok payload: %w", err)
	}
	if err := o.removeMOKHelperEntries(ctx); err != nil {
		return err
	}
	out, err := hostCommandCombinedOutput(ctx, "efibootmgr", "-C", "-d", o.targetDisk, "-p", partNumberFromDevice(o.bootPartition),
		"-L", mokEnrollLabel, "-l", mokHelperESPPath, "-@", payload.Name())
	if err != nil {
		return fmt.Errorf("efibootmgr create %s: %s: %w", mokEnrollLabel, strings.TrimSpace(string(out)), err)
	}
	entries := mokHelperEntries(string(out))
	if len(entries) != 1 {
		return fmt.Errorf("want one %s boot entry, efibootmgr lists %v", mokEnrollLabel, entries)
	}
	if out, err := hostCommandCombinedOutput(ctx, "efibootmgr", "-n", entries[0]); err != nil {
		return fmt.Errorf("efibootmgr BootNext %s: %s: %w", entries[0], strings.TrimSpace(string(out)), err)
	}
	o.log.Info("MOK enrollment staged: the next firmware boot runs mok-enroll.efi", "entry", "Boot"+entries[0])
	return nil
}

// removeMOKHelperEntries deletes helper entries of an earlier, interrupted run.
func (o *Orchestrator) removeMOKHelperEntries(ctx context.Context) error {
	out, err := hostCommandCombinedOutput(ctx, "efibootmgr")
	if err != nil {
		return fmt.Errorf("efibootmgr list: %s: %w", strings.TrimSpace(string(out)), err)
	}
	for _, n := range mokHelperEntries(string(out)) {
		if out, err := hostCommandCombinedOutput(ctx, "efibootmgr", "-b", n, "-B"); err != nil {
			return fmt.Errorf("efibootmgr remove Boot%s: %s: %w", n, strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

// mokHelperEntries returns the boot numbers of helper entries in efibootmgr output.
func mokHelperEntries(out string) []string {
	var nums []string
	for line := range strings.Lines(out) {
		if len(line) > 8 && strings.HasPrefix(line, "Boot") && efiBootEntryLineLabel(line) == mokEnrollLabel {
			nums = append(nums, line[4:8])
		}
	}
	return nums
}

func (o *Orchestrator) secureBootEnabled() (bool, error) {
	return efi.NewEFIVarReader(reportEFIVarsDir).IsSecureBootEnabled()
}
