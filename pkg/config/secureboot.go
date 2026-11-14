package config

// SecureBootConfig manages UEFI Secure Boot lifecycle during provisioning.
// Supports re-enabling Secure Boot after OS installation and MOK enrollment
// for custom kernel modules.
type SecureBootConfig struct {
	// ReEnable signals the CAPRF server to re-enable Secure Boot after provisioning.
	// Default: false
	ReEnable bool `yaml:"reEnable" json:"reEnable"`

	// PinnedDigests enforces SHA256 digests for expected boot artifacts.
	// Keys are component names: "shim", "grub", and "kernel".
	// Values accept bare 64-character hex or "sha256:<hex>".
	// Default: {} (digest enforcement disabled)
	PinnedDigests map[string]string `yaml:"pinnedDigests" json:"pinnedDigests"`

	// Signing selects who signs the out-of-tree kernel modules of the
	// target: per-host (BOOTy, with a key that exists only while
	// provisioning), ci (the image arrives signed; BOOTy only enrolls
	// MOKCertPath) or off. See docs/module-signing.md.
	// Default: off
	Signing string `yaml:"signing" json:"signing"`

	// MOKCertPath is the absolute path in the target of the DER certificate
	// that signed the modules, for Signing ci.
	// Default: ""
	MOKCertPath string `yaml:"mokCertPath" json:"mokCertPath"`

	// MOKPassword is the one-time password for MokManager confirmation on next boot.
	// Default: ""
	MOKPassword string `yaml:"mokPassword" json:"mokPassword"`

	// ModuleSigning configures Signing per-host and ci: enrollment,
	// certificate directory and failure handling.
	ModuleSigning ModuleSigningConfig `yaml:"moduleSigning" json:"moduleSigning"`
}

// Secure Boot signing modes (SecureBootConfig.Signing).
const (
	// SigningOff signs and enrolls nothing: provisioning as without the
	// feature.
	SigningOff = "off"
	// SigningPerHost signs the modules with a new per-host key and enrolls
	// its certificate.
	SigningPerHost = "per-host"
	// SigningCI enrolls MOKCertPath, the certificate of the CI key that
	// signed the modules of the image. BOOTy holds no private key.
	SigningCI = "ci"
)

// SigningMode returns Signing or the default, off.
func (s *SecureBootConfig) SigningMode() string {
	if s.Signing == "" {
		return SigningOff
	}
	return s.Signing
}

// Module signing enrollment methods.
const (
	// ModuleSigningEnrollEFIHelper boots mok-enroll.efi once (BootNext) to
	// write the certificate into MokList. Needs Secure Boot off during
	// provisioning.
	ModuleSigningEnrollEFIHelper = "efi-helper"
	// ModuleSigningEnrollMokutil queues the certificate with mokutil
	// --import; MokManager asks for MOKPassword once at the console.
	ModuleSigningEnrollMokutil = "mokutil"
	// ModuleSigningEnrollExternal only installs and reports the
	// certificate; the provisioner enrolls it, for example into db over
	// Redfish.
	ModuleSigningEnrollExternal = "external"
)

// Module signing targets.
const (
	// ModuleSigningTargetInPlace signs the modules in the target root.
	ModuleSigningTargetInPlace = "in-place"
	// ModuleSigningTargetSysext leaves the target root unchanged and installs
	// the signed modules as a per-host system extension with a dm-verity
	// root hash signed by the same key, for read-only (dm-verity) roots.
	// Experimental.
	ModuleSigningTargetSysext = "sysext"
)

// DefaultModuleSigningCertDir is where the certificate is installed in the
// target.
const DefaultModuleSigningCertDir = "/usr/share/booty/secureboot"

// ModuleSigningConfig controls per-host signing of out-of-tree modules.
type ModuleSigningConfig struct {
	// Enrollment is efi-helper (default), mokutil or external.
	Enrollment string `yaml:"enrollment" json:"enrollment"`
	// Target is in-place (default) or sysext (experimental).
	Target string `yaml:"target" json:"target"`
	// CertDir is the absolute target directory for the DER certificate.
	// Default: /usr/share/booty/secureboot
	CertDir string `yaml:"certDir" json:"certDir"`
	// AllowFailure continues provisioning when signing or enrollment fails.
	// Default: false (fail closed)
	AllowFailure bool `yaml:"allowFailure" json:"allowFailure"`
}

// EnrollmentMethod returns Enrollment or the default.
func (m ModuleSigningConfig) EnrollmentMethod() string {
	if m.Enrollment == "" {
		return ModuleSigningEnrollEFIHelper
	}
	return m.Enrollment
}

// TargetOrDefault returns Target or the default.
func (m ModuleSigningConfig) TargetOrDefault() string {
	if m.Target == "" {
		return ModuleSigningTargetInPlace
	}
	return m.Target
}

// CertDirOrDefault returns CertDir or the default.
func (m ModuleSigningConfig) CertDirOrDefault() string {
	if m.CertDir == "" {
		return DefaultModuleSigningCertDir
	}
	return m.CertDir
}

// NeedsFirmwareReboot reports whether the handoff must go through the
// firmware instead of kexec: Secure Boot is re-enabled, or a module signing
// certificate is enrolled. That takes effect at the next firmware boot with
// every enrollment method.
func (s *SecureBootConfig) NeedsFirmwareReboot() bool {
	// external too: a certificate enrolled in db over Redfish takes effect at
	// the next POST, and kexec would start the kernel without it.
	return s.ReEnable || s.SigningMode() != SigningOff
}
