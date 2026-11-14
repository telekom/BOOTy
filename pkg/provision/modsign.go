//go:build linux

package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

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

// ReportModuleSigning is the per-host module signing result. Cert (DER,
// base64 in JSON) lets the provisioner enroll the certificate itself, for
// example into db over Redfish (enrollment external).
type ReportModuleSigning struct {
	Modules    int    `json:"modules"`
	Enrollment string `json:"enrollment"`
	CertPath   string `json:"certPath"`
	// Target is sysext when the modules are in a system extension
	// (Sysext, with the dm-verity RootHash); empty for in-place signing.
	Target     string `json:"target,omitempty"`
	Sysext     string `json:"sysext,omitempty"`
	RootHash   string `json:"rootHash,omitempty"`
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

// signKernelModules signs the out-of-tree modules of the target with a new
// per-host key, installs the certificate, and rebuilds the module index and
// initramfs. The private key exists only in memory during this step.
func (o *Orchestrator) signKernelModules(ctx context.Context) error {
	ms := o.cfg.Provision.SecureBoot.ModuleSigning
	if !ms.Enabled {
		return nil
	}
	return o.moduleSigningResult("sign kernel modules", o.doSignKernelModules(ctx, ms))
}

func (o *Orchestrator) doSignKernelModules(ctx context.Context, ms config.ModuleSigningConfig) error {
	if ms.TargetOrDefault() == config.ModuleSigningTargetSysext {
		return o.signKernelModulesSysext(ctx, ms)
	}
	root := o.config.rootDir
	res, err := secureboot.SignModuleTree(root, "BOOTy module signing "+o.cfg.Hostname)
	if err != nil {
		return err
	}
	certPath := path.Join(ms.CertDirOrDefault(), moduleCertFile)
	if err := writeTargetRootFile(root, filepath.Join(root, certPath), res.CertDER); err != nil {
		return fmt.Errorf("install module signing certificate: %w", err)
	}
	sum := sha256.Sum256(res.CertDER)
	o.moduleSigning = &ReportModuleSigning{
		Modules: len(res.Modules), Enrollment: ms.EnrollmentMethod(),
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
// sign-kernel-modules created in this run. It never enrolls a certificate
// read from the image: with MODULE_SIGNING_ALLOW_FAILURE a failed signing
// leaves whatever the image ships at the certificate path, for a key its
// author holds. sign-kernel-modules runs again on a resume for the same
// reason.
func (o *Orchestrator) enrollModuleSigningKey(ctx context.Context) error {
	ms := o.cfg.Provision.SecureBoot.ModuleSigning
	if !ms.Enabled {
		return nil
	}
	return o.moduleSigningResult("enroll module signing key", o.doEnrollModuleSigningKey(ctx, ms))
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
		// mokutil runs on the ramdisk, so give it a copy of the certificate
		// of this run rather than a path inside the image.
		f, err := os.CreateTemp("", "booty-module-signing-*.der")
		if err != nil {
			return fmt.Errorf("module signing certificate: %w", err)
		}
		defer func() { _ = os.Remove(f.Name()) }()
		_, err = f.Write(cert)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("write module signing certificate: %w", err)
		}
		return secureboot.NewMOKEnroller(f.Name(), o.cfg.Provision.SecureBoot.MOKPassword).Enroll(ctx)
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
