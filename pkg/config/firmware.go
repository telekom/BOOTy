package config

// FirmwareConfig controls hardware firmware version reporting and enforcement.
// When enabled, BOOTy collects firmware versions (BIOS, BMC) and reports them
// to the configured endpoint. Minimum version checks can block provisioning.
type FirmwareConfig struct {
	// Enabled activates firmware collection and reporting.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// URL is the POST endpoint for the firmware version report.
	// Default: ""
	URL string `yaml:"url" json:"url"`

	// MinBIOS is the minimum acceptable BIOS/UEFI firmware version.
	// Provisioning fails if the detected version is older.
	// Default: "" (no minimum)
	MinBIOS string `yaml:"minBIOS" json:"minBIOS"`

	// MinBMC is the minimum acceptable BMC/iLO/iDRAC firmware version.
	// Default: "" (no minimum)
	MinBMC string `yaml:"minBMC" json:"minBMC"`
}
