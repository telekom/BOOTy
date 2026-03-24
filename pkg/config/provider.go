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
	DiskDevice        string   // DISK_DEVICE: override disk detection (e.g. "/dev/sda", "/dev/loop0")
	NumVFs            int      // NUM_VFS: number of SR-IOV VFs for Mellanox (default: 32)
	DisableKexec      bool     // DISABLE_KEXEC: skip kexec and always hard-reboot
	SecureErase       bool     // SECURE_ERASE: use ATA/NVMe secure erase instead of wipefs
	PostProvisionCmds []string // POST_PROVISION_CMDS: commands to run in chroot after provisioning

	// Image verification fields.
	ImageSignatureURL string // IMAGE_SIGNATURE_URL: detached GPG signature URL
	ImageGPGPubKey    string // IMAGE_GPG_PUBKEY: path to GPG public key for image verification

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
	ProvisionGateway string // provision_gateway: gateway VTEP IP for BUM flooding
	DNSResolvers     string // dns_resolver: comma-separated DNS servers
	DCGWIPs          string // dcgw_ips: Data Center Gateway IPs (onefabric)
	LeafASN          uint32 // leaf_asn: Leaf switch AS
	LocalASN         uint32 // local_asn: Local AS for leaf connections
	OverlayAggregate string // overlay_aggregate: route aggregate for overlay
	VPNRT            string // vpn_rt: VPN route target for EVPN

	// Static networking fields.
	StaticIP      string // STATIC_IP: IP/mask to assign (e.g. "10.0.0.5/24")
	StaticGateway string // STATIC_GATEWAY: default gateway IP
	StaticIface   string // STATIC_IFACE: interface name (default: auto-detect first physical NIC)

	// BGP/BFD tuning fields.
	VRFTableID    uint32 // vrf_table_id: routing table ID for VRF (default: 1)
	BGPKeepalive  uint32 // bgp_keepalive: BGP keepalive interval in seconds (0 = FRR default)
	BGPHold       uint32 // bgp_hold: BGP hold timer in seconds (0 = FRR default)
	BFDTransmitMS uint32 // bfd_transmit_ms: BFD transmit interval in ms (default: 300)
	BFDReceiveMS  uint32 // bfd_receive_ms: BFD receive interval in ms (default: 300)

	// Firmware reporting fields.
	FirmwareEnabled bool   // FIRMWARE_REPORT: enable firmware collection
	FirmwareURL     string // FIRMWARE_URL: endpoint for firmware report
	FirmwareMinBIOS string // FIRMWARE_MIN_BIOS: minimum BIOS version
	FirmwareMinBMC  string // FIRMWARE_MIN_BMC: minimum BMC version

	// LACP bonding fields.
	BondInterfaces string // BOND_INTERFACES: comma-separated NICs to bond (e.g. "eth0,eth1")
	BondMode       string // BOND_MODE: bonding mode (default: "802.3ad")

	// VLAN fields.
	VLANs string // VLANS: multi-VLAN config "200:eno1:10.200.0.42/24,300:eno2"

	// Hardware inventory fields.
	InventoryEnabled bool   // INVENTORY_ENABLED: collect and report hardware inventory
	InventoryURL     string // INVENTORY_URL: POST endpoint for inventory JSON

	// Health check configuration.
	HealthChecksEnabled bool   // HEALTH_CHECKS_ENABLED: run pre-provision health checks
	HealthMinMemoryGB   int    // HEALTH_MIN_MEMORY_GB: minimum RAM in GiB (name kept for compatibility)
	HealthMinCPUs       int    // HEALTH_MIN_CPUS: minimum CPU count
	HealthSkipChecks    string // HEALTH_SKIP_CHECKS: comma-separated check names to skip
	HealthCheckURL      string // HEALTH_CHECK_URL: POST endpoint for health results

	// BGP peering mode and numbered peer configuration.
	BGPPeerMode  string // BGP_PEER_MODE: "unnumbered" (default), "dual", "numbered"
	BGPNeighbors string // BGP_NEIGHBORS: comma-separated numbered peer IPs
	BGPRemoteASN uint32 // bgp_remote_asn: remote ASN for numbered peers (0 = iBGP)

	// Dry-run mode.
	DryRun bool // DRY_RUN: simulate provisioning without destructive changes

	// Network mode override.
	NetworkMode string // NETWORK_MODE: "gobgp" to use in-process GoBGP instead of FRR

	// Telemetry configuration.
	TelemetryEnabled bool   // TELEMETRY_ENABLED: enable provisioning metrics collection
	TelemetryURL     string // TELEMETRY_URL: POST endpoint for metrics snapshot

	// Observability fields.
	MetricsURL string // METRICS_URL: POST endpoint for provisioning metrics
	EventURL   string // EVENT_URL: POST endpoint for provisioning events

	// SecureBoot lifecycle fields.
	SecureBootReEnable bool   // SECUREBOOT_REENABLE: signal CAPRF to re-enable SecureBoot after provisioning
	MOKCertPath        string // MOK_CERT_PATH: path to DER-encoded MOK certificate for enrollment
	MOKPassword        string // MOK_PASSWORD: one-time password for MokManager confirmation

	// Rescue mode configuration.
	RescueMode           string // RESCUE_MODE: "reboot" (default), "retry", "shell", "wait"
	RescueSSHPubKey      string // RESCUE_SSH_PUBKEY: authorized SSH public key for rescue shell
	RescuePasswordHash   string // RESCUE_PASSWORD_HASH: crypt(3) password hash for rescue shell
	RescueTimeout        int    // RESCUE_TIMEOUT: seconds before rescue auto-action, 0 = infinite
	RescueAutoMountDisks bool   // RESCUE_AUTO_MOUNT: auto-mount discovered disks in rescue mode

	// EVPN L2 overlay (Type-2/3 route processing) — disabled by default.
	EVPNL2Enabled bool // EVPN_L2_ENABLED: enable Type-2/3 route handling for L2 overlay

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
	// AcknowledgeCommand reports command execution result back to the server.
	AcknowledgeCommand(ctx context.Context, cmdID, status, message string) error
	// ReportInventory sends hardware inventory data to the server.
	ReportInventory(ctx context.Context, data []byte) error
	// ReportFirmware sends a firmware report to the server.
	ReportFirmware(ctx context.Context, data []byte) error
}
