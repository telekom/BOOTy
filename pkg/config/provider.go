// Package config defines the provisioning configuration provider interface.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

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
	ImageMode         string   // IMAGE_MODE: "whole-disk" (default) or "partition" for partition-by-partition
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

	// Declarative disk partitioning (JSON from PARTITION_LAYOUT).
	PartitionLayout *PartitionLayout
}

// PartitionLayout defines a declarative partitioning scheme for the target disk.
type PartitionLayout struct {
	Table      string      `json:"table"`            // "gpt" (default: "gpt") — only GPT is supported
	Device     string      `json:"device,omitempty"` // Device override (empty = auto-detect)
	Partitions []Partition `json:"partitions"`       // Ordered list of partitions to create
	LVM        *LVMConfig  `json:"lvm,omitempty"`    // Optional LVM configuration
}

// Partition defines a single partition in a PartitionLayout.
type Partition struct {
	Label      string `json:"label"`                // GPT partition label (e.g. "efi", "root", "data")
	SizeMB     int    `json:"sizeMB,omitempty"`     // Size in MiB (0 = fill remaining space)
	TypeGUID   string `json:"typeGUID,omitempty"`   // GPT type GUID (auto-set from fsType if omitted)
	Filesystem string `json:"filesystem,omitempty"` // mkfs type: "vfat", "ext4", "xfs", "swap"
	Mountpoint string `json:"mountpoint,omitempty"` // Target mount path (e.g. "/", "/boot/efi")
}

// LVMConfig defines LVM volume group and logical volume configuration.
type LVMConfig struct {
	VolumeGroup string     `json:"volumeGroup"` // VG name (e.g. "sysvg")
	PVPartition int        `json:"pvPartition"` // 1-based partition index for the PV
	Volumes     []LVVolume `json:"volumes"`     // Logical volumes to create
}

// LVVolume defines a single logical volume within an LVM volume group.
type LVVolume struct {
	Name       string `json:"name"`                 // LV name (e.g. "root", "var")
	SizeMB     int    `json:"sizeMB,omitempty"`     // Size in MiB (0 = fill remaining)
	Extents    string `json:"extents,omitempty"`    // Size as extents (e.g. "100%FREE")
	Filesystem string `json:"filesystem,omitempty"` // mkfs type
	Mountpoint string `json:"mountpoint,omitempty"` // Target mount path
}

// ParsePartitionLayout parses a JSON partition layout string.
func ParsePartitionLayout(data string) (*PartitionLayout, error) {
	var layout PartitionLayout
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&layout); err != nil {
		return nil, fmt.Errorf("parsing partition layout: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parsing partition layout: unexpected trailing content")
	}

	if len(layout.Partitions) == 0 {
		return nil, fmt.Errorf("partition layout has no partitions")
	}
	if layout.Table == "" {
		layout.Table = "gpt"
	}
	if layout.Table != "gpt" {
		return nil, fmt.Errorf("unsupported partition table %q, only \"gpt\" is supported", layout.Table)
	}
	device, err := normalizePartitionLayoutDevice(layout.Device)
	if err != nil {
		return nil, err
	}
	layout.Device = device
	if err := validatePartitions(layout.Partitions); err != nil {
		return nil, err
	}
	if err := validateLVMConfig(layout.LVM, layout.Partitions); err != nil {
		return nil, err
	}
	if err := validateUniqueMountpoints(layout.Partitions, layout.LVM); err != nil {
		return nil, err
	}
	if err := validateRootPresence(layout.Partitions, layout.LVM); err != nil {
		return nil, err
	}
	return &layout, nil
}

func normalizePartitionLayoutDevice(device string) (string, error) {
	trimmed := strings.TrimSpace(device)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsAny(trimmed, " \t\n\r") {
		return "", fmt.Errorf("partition layout device %q must not contain whitespace", device)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("partition layout device %q must be an absolute path", device)
	}
	return trimmed, nil
}

// validatePartitions checks partition definitions for required fields.
func validatePartitions(partitions []Partition) error {
	fillCount := 0
	seen := make(map[string]bool)
	for i, part := range partitions {
		if err := validatePartitionEntry(i, part, len(partitions), seen); err != nil {
			return err
		}
		if part.SizeMB == 0 {
			fillCount++
		}
	}
	if fillCount > 1 {
		return fmt.Errorf("only one partition may use sizeMB=0 (fill remaining), got %d", fillCount)
	}
	return nil
}

func validatePartitionEntry(index int, part Partition, partitionCount int, seen map[string]bool) error {
	if err := validatePartitionLabel(index, part.Label, seen); err != nil {
		return err
	}
	if err := validatePartitionMountpoint(index, part); err != nil {
		return err
	}
	if !isSupportedFilesystem(part.Filesystem) {
		return fmt.Errorf("partition %d (%s): unsupported filesystem %q", index+1, part.Label, part.Filesystem)
	}
	if err := validatePartitionSize(index, part, partitionCount); err != nil {
		return err
	}
	return nil
}

func validatePartitionLabel(index int, label string, seen map[string]bool) error {
	if label == "" {
		return fmt.Errorf("partition %d: label is required", index+1)
	}
	if !isValidPartitionLabel(label) {
		return fmt.Errorf("partition %d: label %q contains invalid characters or exceeds 36 characters", index+1, label)
	}
	if seen[label] {
		return fmt.Errorf("partition %d: duplicate label %q", index+1, label)
	}
	seen[label] = true
	return nil
}

func validatePartitionMountpoint(index int, part Partition) error {
	if part.Mountpoint != "" && !strings.HasPrefix(part.Mountpoint, "/") {
		return fmt.Errorf("partition %d (%s): mountpoint %q must be an absolute path", index+1, part.Label, part.Mountpoint)
	}
	if strings.ContainsAny(part.Mountpoint, " \t\n\r") {
		return fmt.Errorf("partition %d (%s): mountpoint %q must not contain whitespace", index+1, part.Label, part.Mountpoint)
	}
	if part.Mountpoint != "" && part.Filesystem == "" {
		return fmt.Errorf("partition %d (%s): mountpoint %q requires a filesystem", index+1, part.Label, part.Mountpoint)
	}
	if part.Filesystem == "swap" && part.Mountpoint != "" {
		return fmt.Errorf("partition %d (%s): swap partition must not define mountpoint %q", index+1, part.Label, part.Mountpoint)
	}
	return nil
}

func validatePartitionSize(index int, part Partition, partitionCount int) error {
	if part.SizeMB < 0 {
		return fmt.Errorf("partition %d (%s): sizeMB must be non-negative", index+1, part.Label)
	}
	if part.SizeMB == 0 && index != partitionCount-1 {
		return fmt.Errorf("partition %d (%s): sizeMB=0 (fill remaining) must be the last partition", index+1, part.Label)
	}
	return nil
}

func validateLVMConfig(lvm *LVMConfig, partitions []Partition) error {
	if lvm == nil {
		return nil
	}
	if err := validateLVMPVPartition(lvm, partitions); err != nil {
		return err
	}
	seenNames := make(map[string]bool)
	for i, vol := range lvm.Volumes {
		if err := validateLVMVolume(i, vol); err != nil {
			return err
		}
		if seenNames[vol.Name] {
			return fmt.Errorf("lvm volume %d: duplicate name %q", i+1, vol.Name)
		}
		seenNames[vol.Name] = true
		if usesAllRemainingLVMExtents(vol) && i != len(lvm.Volumes)-1 {
			return fmt.Errorf("lvm volume %d (%s): fill-remaining volume must be the last lvm volume", i+1, vol.Name)
		}
	}
	return nil
}

func usesAllRemainingLVMExtents(vol LVVolume) bool {
	if vol.SizeMB > 0 {
		return false
	}
	extents := strings.TrimSpace(vol.Extents)
	return extents == "" || strings.EqualFold(extents, "100%FREE")
}

func validateUniqueMountpoints(partitions []Partition, lvm *LVMConfig) error {
	seen := make(map[string]string)

	addMountpoint := func(mountpoint, location string) error {
		if mountpoint == "" {
			return nil
		}
		if prev, ok := seen[mountpoint]; ok {
			return fmt.Errorf("mountpoint %q is defined multiple times (%s, %s)", mountpoint, prev, location)
		}
		seen[mountpoint] = location
		return nil
	}

	for i, part := range partitions {
		location := fmt.Sprintf("partition %d (%s)", i+1, part.Label)
		if err := addMountpoint(part.Mountpoint, location); err != nil {
			return err
		}
	}

	if lvm == nil {
		return nil
	}

	for i, vol := range lvm.Volumes {
		location := fmt.Sprintf("lvm volume %d (%s)", i+1, vol.Name)
		if err := addMountpoint(vol.Mountpoint, location); err != nil {
			return err
		}
	}

	return nil
}

func validateLVMPVPartition(lvm *LVMConfig, partitions []Partition) error {
	if lvm.VolumeGroup == "" {
		return fmt.Errorf("lvm: volumeGroup is required")
	}
	if !isValidLVMName(lvm.VolumeGroup) {
		return fmt.Errorf("lvm: invalid volumeGroup name %q", lvm.VolumeGroup)
	}
	if lvm.PVPartition < 1 {
		return fmt.Errorf("lvm: pvPartition must be >= 1, got %d", lvm.PVPartition)
	}
	if lvm.PVPartition > len(partitions) {
		return fmt.Errorf("lvm: pvPartition %d exceeds partition count %d", lvm.PVPartition, len(partitions))
	}

	pvPart := partitions[lvm.PVPartition-1]
	if pvPart.Mountpoint != "" {
		return fmt.Errorf("lvm: pvPartition %d (%s) must not define mountpoint %q", lvm.PVPartition, pvPart.Label, pvPart.Mountpoint)
	}
	if pvPart.Filesystem != "" {
		return fmt.Errorf("lvm: pvPartition %d (%s) must not define filesystem %q", lvm.PVPartition, pvPart.Label, pvPart.Filesystem)
	}
	return nil
}

func validateLVMVolume(index int, vol LVVolume) error {
	if vol.Name == "" {
		return fmt.Errorf("lvm volume %d: name is required", index+1)
	}
	if !isValidLVMName(vol.Name) {
		return fmt.Errorf("lvm volume %d: invalid name %q", index+1, vol.Name)
	}
	if err := validateLVMVolumeMountpoint(index, vol); err != nil {
		return err
	}
	if !isSupportedFilesystem(vol.Filesystem) {
		return fmt.Errorf("lvm volume %d (%s): unsupported filesystem %q", index+1, vol.Name, vol.Filesystem)
	}
	if err := validateLVMVolumeSize(index, vol); err != nil {
		return err
	}
	return nil
}

func validateLVMVolumeMountpoint(index int, vol LVVolume) error {
	if vol.Mountpoint != "" && !strings.HasPrefix(vol.Mountpoint, "/") {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q must be an absolute path", index+1, vol.Name, vol.Mountpoint)
	}
	if strings.ContainsAny(vol.Mountpoint, " \t\n\r") {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q must not contain whitespace", index+1, vol.Name, vol.Mountpoint)
	}
	if vol.Mountpoint != "" && vol.Filesystem == "" {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q requires a filesystem", index+1, vol.Name, vol.Mountpoint)
	}
	if vol.Filesystem == "swap" && vol.Mountpoint != "" {
		return fmt.Errorf("lvm volume %d (%s): swap volume must not define mountpoint %q", index+1, vol.Name, vol.Mountpoint)
	}
	return nil
}

func validateLVMVolumeSize(index int, vol LVVolume) error {
	if vol.SizeMB < 0 {
		return fmt.Errorf("lvm volume %d (%s): sizeMB must be non-negative", index+1, vol.Name)
	}
	if vol.Extents != "" && vol.SizeMB > 0 {
		return fmt.Errorf("lvm volume %d (%s): specify either sizeMB or extents, not both", index+1, vol.Name)
	}
	if vol.Extents != "" && !isValidLVMExtents(vol.Extents) {
		return fmt.Errorf("lvm volume %d (%s): invalid extents format %q", index+1, vol.Name, vol.Extents)
	}
	return nil
}

func validateRootPresence(partitions []Partition, lvm *LVMConfig) error {
	for _, part := range partitions {
		if part.Mountpoint == "/" {
			return nil
		}
	}
	if lvm != nil {
		for _, vol := range lvm.Volumes {
			if vol.Mountpoint == "/" {
				return nil
			}
		}
	}
	return fmt.Errorf("partition layout must include mountpoint \"/\" in either a partition or an lvm volume")
}

func isSupportedFilesystem(fs string) bool {
	switch fs {
	case "", "vfat", "ext4", "xfs", "swap":
		return true
	default:
		return false
	}
}

// isValidPartitionLabel checks that a label is safe for GPT and fstab use.
// GPT labels are limited to 36 UTF-16 characters; only printable ASCII
// (alphanumeric, space, hyphen, underscore, dot) is accepted.
func isValidPartitionLabel(label string) bool {
	if len(label) > 36 {
		return false
	}
	for _, c := range label {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') &&
			c != '_' && c != '-' && c != '.' && c != ' ' {
			return false
		}
	}
	return true
}

// isValidLVMName checks that a name contains only safe characters for LVM identifiers.
func isValidLVMName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	// Reject option-like and hidden-like names to avoid CLI arg ambiguity.
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return false
	}
	for _, c := range name {
		if !isValidLVMNameChar(c) {
			return false
		}
	}
	return true
}

func isValidLVMNameChar(c rune) bool {
	if c >= 'a' && c <= 'z' {
		return true
	}
	if c >= 'A' && c <= 'Z' {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	return c == '_' || c == '-' || c == '.'
}

// isValidLVMExtents checks that an extents value contains only characters
// valid for lvcreate -l arguments (digits, letters, %, +).
func isValidLVMExtents(extents string) bool {
	if extents == "" {
		return false
	}
	for _, c := range extents {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '%' && c != '+' {
			return false
		}
	}
	return true
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
