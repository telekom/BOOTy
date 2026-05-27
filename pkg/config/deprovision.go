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
}
