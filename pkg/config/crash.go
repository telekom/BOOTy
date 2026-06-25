package config

// CrashArtifactsConfig controls pre-provisioning crash artifact collection.
// Before destructive disk operations, BOOTy can inspect an existing OS
// installation for crash dumps, logs, and other forensic artifacts.
type CrashArtifactsConfig struct {
	// Enabled activates crash artifact inspection before provisioning.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// PrepareURL is the CAPRF endpoint that returns upload instructions.
	// Default: ""
	PrepareURL string `yaml:"prepareURL" json:"prepareURL"`

	// UploadURL is the direct upload endpoint for crash artifact archives.
	// Default: ""
	UploadURL string `yaml:"uploadURL" json:"uploadURL"`

	// MaxMB is the maximum archive payload size in MiB.
	// Default: 256
	MaxMB int `yaml:"maxMB" json:"maxMB"`

	// UploadTimeoutSec is the upload timeout in seconds.
	// Default: 120
	UploadTimeoutSec int `yaml:"uploadTimeoutSec" json:"uploadTimeoutSec"`

	// IncludeMemoryDumps allows raw vmcore and systemd coredump uploads.
	// Default: false
	IncludeMemoryDumps bool `yaml:"includeMemoryDumps" json:"includeMemoryDumps"`
}
