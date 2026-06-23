package config

// CloudInitConfig controls cloud-init configuration generation and injection
// into the provisioned OS.
type CloudInitConfig struct {
	// Enabled activates cloud-init config generation.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Datasource is the cloud-init datasource type to configure.
	// Valid values: "nocloud"
	// Default: "nocloud"
	Datasource string `yaml:"datasource" json:"datasource"`
}
