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
}
