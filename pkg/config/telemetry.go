package config

// TelemetryConfig controls observability data collection and reporting.
// Metrics and events are POSTed to the configured endpoints during provisioning.
type TelemetryConfig struct {
	// Enabled activates provisioning metrics collection.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// URL is the POST endpoint for telemetry metric snapshots.
	// Default: ""
	URL string `yaml:"url" json:"url"`

	// MetricsURL is the POST endpoint for structured provisioning metrics.
	// Default: ""
	MetricsURL string `yaml:"metricsURL" json:"metricsURL"`

	// EventURL is the POST endpoint for provisioning lifecycle events.
	// Default: ""
	EventURL string `yaml:"eventURL" json:"eventURL"`
}
