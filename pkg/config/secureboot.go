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

	// MOKCertPath is the path to a DER-encoded MOK certificate for enrollment.
	// The certificate is enrolled via mokutil so the installed OS trusts
	// custom-signed kernel modules.
	// Default: ""
	MOKCertPath string `yaml:"mokCertPath" json:"mokCertPath"`

	// MOKPassword is the one-time password for MokManager confirmation on next boot.
	// Default: ""
	MOKPassword string `yaml:"mokPassword" json:"mokPassword"`

	// ModuleSigning signs out-of-tree kernel modules of the target with a
	// per-host key and enrolls its certificate. See docs/module-signing.md.
	ModuleSigning ModuleSigningConfig `yaml:"moduleSigning" json:"moduleSigning"`
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
	// Enabled signs every module below /lib/modules/*/{updates,extra}.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`
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
// firmware instead of kexec: Secure Boot is re-enabled, or a MOK enrollment
// runs at the next firmware boot.
func (s *SecureBootConfig) NeedsFirmwareReboot() bool {
	m := s.ModuleSigning
	return s.ReEnable || (m.Enabled && m.EnrollmentMethod() != ModuleSigningEnrollExternal)
}
