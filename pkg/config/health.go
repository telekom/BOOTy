package config

// HealthConfig controls pre-provisioning hardware health checks.
// Health checks run before destructive provisioning operations and can
// also be executed standalone via the "check" operating mode.
type HealthConfig struct {
	// Enabled activates the health check suite.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// MinMemoryGB is the minimum required RAM in GiB.
	// Machines with less memory will fail the memory check.
	// Default: 0 (no minimum)
	MinMemoryGB int `yaml:"minMemoryGB" json:"minMemoryGB"`

	// MinCPUs is the minimum required CPU core count.
	// Default: 0 (no minimum)
	MinCPUs int `yaml:"minCPUs" json:"minCPUs"`

	// SkipChecks is a comma-separated list of check names to skip.
	// Example: "thermal,disk"
	// Default: "" (run all checks)
	SkipChecks string `yaml:"skipChecks" json:"skipChecks"`

	// ReportURL is the POST endpoint for health check results JSON.
	// Default: ""
	ReportURL string `yaml:"reportURL" json:"reportURL"`
}
