package config

// DeprovisionConfig holds settings specific to the deprovisioning pipeline.
// This section is only read when Mode is "deprovision" or "soft-deprovision".
type DeprovisionConfig struct {
	// SecureErase uses ATA Secure Erase or NVMe Format for disk sanitization
	// during deprovisioning. More thorough than wipefs but slower.
	// Default: false
	SecureErase bool `yaml:"secureErase" json:"secureErase"`

	// Device overrides automatic disk detection for deprovisioning.
	// Example: "/dev/sda"
	// Default: "" (auto-detect)
	Device string `yaml:"device" json:"device"`

	// CryptoErase destroys every LUKS2 header on the disks that hard
	// deprovisioning and decommission wipe (cryptsetup luksErase, then
	// zeros over both header copies and the keyslots area) before the
	// wipe. A failure fails the run. false skips it.
	// Default: nil (true)
	CryptoErase *bool `yaml:"cryptoErase,omitempty" json:"cryptoErase,omitempty"`
}
