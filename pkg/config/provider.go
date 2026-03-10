// Package config defines the provisioning configuration provider interface.
package config

import "context"

// Status represents the provisioning status reported to the server.
type Status string

// Provisioning status constants.
const (
	StatusInit    Status = "init"
	StatusSuccess Status = "success"
	StatusError   Status = "error"
)

// MachineConfig holds all configuration needed for provisioning a machine.
type MachineConfig struct {
	ImageURLs         []string // Space-separated IMAGE field from /deploy/vars
	Hostname          string   // HOSTNAME
	Token             string   // TOKEN (Bearer auth for CAPRF server)
	ExtraKernelParams string   // MACHINE_EXTRA_KERNEL_PARAMS
	FailureDomain     string   // FAILURE_DOMAIN (topology.kubernetes.io/zone)
	Region            string   // REGION
	ProviderID        string   // PROVIDER_ID (kubelet --provider-id)
	Mode              string   // MODE: "provision", "deprovision", "soft-deprovision"
	MinDiskSizeGB     int      // MIN_DISK_SIZE_GB (optional, 0 = no minimum)

	// Status URLs parsed from /deploy/vars.
	LogURL     string
	InitURL    string
	ErrorURL   string
	SuccessURL string
	DebugURL   string

	// Files and commands from ISO /deploy/ directories.
	ProvisionerFiles []string // Paths to files in /deploy/file-system/
	MachineFiles     []string // Paths to files in /deploy/machine-files/
	MachineCommands  []string // Commands from /deploy/machine-commands/
}

// Command represents a server-issued command (future agent mode).
type Command struct {
	ID      string
	Type    string
	Payload []byte
}

// Provider abstracts provisioning server communication.
type Provider interface {
	// GetConfig fetches machine configuration.
	GetConfig(ctx context.Context) (*MachineConfig, error)
	// ReportStatus sends provisioning status to the server.
	ReportStatus(ctx context.Context, status Status, message string) error
	// ShipLog sends a log line to the server.
	ShipLog(ctx context.Context, message string) error
	// Heartbeat sends a keepalive signal (no-op in current mode, future agent mode).
	Heartbeat(ctx context.Context) error
	// FetchCommands retrieves pending commands (nil in current mode, future agent mode).
	FetchCommands(ctx context.Context) ([]Command, error)
}
