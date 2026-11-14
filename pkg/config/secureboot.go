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

	// Signing selects who signs the boot artifacts that BOOTy installs:
	// "off", "per-host" (BOOTy makes a key per host, signs, and wipes the
	// key) or "ci" (the image arrives signed; BOOTy only verifies and
	// enrolls the public material). Used by the disk encryption mode
	// luks2-tpm2-uki (docs/disk-encryption.md#signed-uki-and-pcr-11).
	// Default: "off"
	Signing string `yaml:"signing" json:"signing"`
}

// Secure Boot signing modes (SecureBootConfig.Signing).
const (
	SecureBootSigningOff     = "off"
	SecureBootSigningPerHost = "per-host"
	SecureBootSigningCI      = "ci"
)

// SigningMode returns Signing or the default "off".
func (s *SecureBootConfig) SigningMode() string {
	if s.Signing == "" {
		return SecureBootSigningOff
	}
	return s.Signing
}
