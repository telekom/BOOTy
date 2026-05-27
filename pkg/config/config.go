// Package config defines the provisioning configuration types and interfaces.
package config

// Config is the top-level configuration for a BOOTy machine.
// It contains the machine identity, operating mode selector, and
// references to cross-cutting and per-mode configuration sections.
//
// Cross-cutting sections (Network, Transport, Health, Telemetry, Rescue)
// are shared across all modes. Per-mode sections (Provision, Deprovision, Agent)
// contain settings specific to that mode, but some paths (e.g. disk detection)
// currently reuse Provision.Disk for consistency across modes.
type Config struct {
	// Hostname is the machine's network hostname, used for logging,
	// JWT token acquisition, and status reporting.
	// Required for all modes.
	Hostname string `yaml:"hostname" json:"hostname"`

	// Mode selects the operating mode. Determines which per-mode config
	// section is active.
	// Valid values: "provision", "deprovision", "soft-deprovision",
	//              "standby", "dry-run", "check"
	// Legacy aliases: "soft" (= soft-deprovision), "hard" (= deprovision)
	// Default: "provision"
	Mode string `yaml:"mode" json:"mode"`

	// DryRun enables dry-run behavior globally, overriding Mode to "dry-run".
	// When true, no destructive operations are performed.
	// Default: false
	DryRun bool `yaml:"dryRun" json:"dryRun"`

	// Cross-cutting configuration sections.
	Network   NetworkConfig   `yaml:"network"   json:"network"`
	Transport TransportConfig `yaml:"transport" json:"transport"`
	Health    HealthConfig    `yaml:"health"    json:"health"`
	Telemetry TelemetryConfig `yaml:"telemetry" json:"telemetry"`
	Rescue    RescueConfig    `yaml:"rescue"    json:"rescue"`

	// Per-mode configuration sections.
	Provision   ProvisionConfig   `yaml:"provision"   json:"provision"`
	Deprovision DeprovisionConfig `yaml:"deprovision" json:"deprovision"`
	Agent       AgentConfig       `yaml:"agent"       json:"agent"`

	// ISO-injected file lists (populated by the CAPRF client, not from config files).
	ProvisionerFiles []string `yaml:"-" json:"-"`
	MachineFiles     []string `yaml:"-" json:"-"`
	MachineCommands  []string `yaml:"-" json:"-"`
}

// MachineConfig is a type alias for backward compatibility during migration.
// New code should use Config directly.
type MachineConfig = Config
