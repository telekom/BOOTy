package config

// InventoryConfig controls hardware inventory collection and reporting.
// When enabled, BOOTy collects detailed hardware information (CPU, memory,
// storage, NICs) and POSTs it as JSON to the configured endpoint.
type InventoryConfig struct {
	// Enabled activates hardware inventory collection.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// URL is the POST endpoint for hardware inventory JSON.
	// Default: ""
	URL string `yaml:"url" json:"url"`
}
