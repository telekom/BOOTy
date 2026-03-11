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
	ImageChecksum     string   // IMAGE_CHECKSUM: expected hex digest of the raw image
	ImageChecksumType string   // IMAGE_CHECKSUM_TYPE: "sha256" or "sha512"
	Hostname          string   // HOSTNAME
	Token             string   // TOKEN (Bearer auth for CAPRF server)
	ExtraKernelParams string   // MACHINE_EXTRA_KERNEL_PARAMS
	FailureDomain     string   // FAILURE_DOMAIN (topology.kubernetes.io/zone)
	Region            string   // REGION
	ProviderID        string   // PROVIDER_ID (kubelet --provider-id)
	Mode              string   // MODE: "provision", "deprovision", "soft-deprovision"
	MinDiskSizeGB     int      // MIN_DISK_SIZE_GB (optional, 0 = no minimum)
	NumVFs            int      // NUM_VFS: number of SR-IOV VFs for Mellanox (default: 32)
	DisableKexec      bool     // DISABLE_KEXEC: skip kexec and always hard-reboot

	// Status URLs parsed from /deploy/vars.
	LogURL       string
	InitURL      string
	ErrorURL     string
	SuccessURL   string
	DebugURL     string
	HeartbeatURL string // POST /status/heartbeat
	CommandsURL  string // GET /commands

	// Network configuration (from kernel cmdline or /deploy/vars).
	UnderlaySubnet   string // underlay_subnet: e.g. "192.168.4.0/24"
	UnderlayIP       string // underlay_ip: direct underlay loopback IP
	OverlaySubnet    string // overlay_subnet: e.g. "2a01:598:40a:5481::/64"
	IPMISubnet       string // ipmi_subnet: e.g. "172.30.0.0/24"
	ASN              uint32 // asn_server: BGP AS number
	ProvisionVNI     uint32 // provision_vni: VXLAN VNI
	ProvisionIP      string // provision_ip: IP/mask for provision bridge
	DNSResolvers     string // dns_resolver: comma-separated DNS servers
	DCGWIPs          string // dcgw_ips: Data Center Gateway IPs (onefabric)
	LeafASN          uint32 // leaf_asn: Leaf switch AS
	LocalASN         uint32 // local_asn: Local AS for leaf connections
	OverlayAggregate string // overlay_aggregate: route aggregate for overlay
	VPNRT            string // vpn_rt: VPN route target for EVPN

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
