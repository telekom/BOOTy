// Package network provides network mode abstractions for provisioning.
package network

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Mode configures network connectivity in the ramdisk.
type Mode interface {
	// Setup configures all networking (interfaces, routing, etc.)
	Setup(ctx context.Context, cfg *Config) error
	// WaitForConnectivity blocks until network is reachable.
	WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error
	// Teardown cleans up network configuration.
	Teardown(ctx context.Context) error
}

// Config holds all parameters needed for network setup.
type Config struct {
	// DHCP mode fields.
	Interfaces []string // NICs to configure (default: auto-detect)

	// FRR/EVPN mode fields (from kernel cmdline or /deploy/vars).
	UnderlaySubnet string // e.g. "192.168.4.0/24"
	UnderlayIP     string // Direct underlay IP (if no subnet)
	OverlaySubnet  string // e.g. "2a01:598:40a:5481::/64"
	IPMISubnet     string // e.g. "172.30.0.0/24"
	IPMIMAC        string // IPMI MAC for IP derivation
	IPMIIP         string // IPMI IP for offset calculation
	ASN            uint32 // BGP ASN for underlay
	ProvisionVNI   uint32 // VXLAN VNI for provision network
	ProvisionIP    string // IP/mask to assign to provision bridge (e.g. "10.100.0.20/24")
	DNSResolvers   string // Comma-separated DNS servers

	// Optional FRR onefabric mode fields.
	DCGWIPs          string // Data Center Gateway IPs (comma-sep)
	LeafASN          uint32 // Leaf switch AS
	LocalASN         uint32 // Local AS for leaf connections
	OverlayAggregate string // Route aggregate for overlay
	VPNRT            string // VPN route target for EVPN

	// Static networking fields.
	StaticIP      string // IP/mask to assign (e.g. "10.0.0.5/24")
	StaticGateway string // Default gateway IP
	StaticIface   string // Interface name (default: auto-detect first physical NIC)

	// LACP bonding fields.
	BondInterfaces string // Comma-separated NICs to bond (e.g. "eth0,eth1")
	BondMode       string // Bonding mode (default: "802.3ad" for LACP)

	// BGP/BFD tuning fields.
	VRFTableID    uint32 // Routing table ID for VRF (default: 1)
	BGPKeepalive  uint32 // BGP keepalive interval in seconds (0 = FRR default)
	BGPHold       uint32 // BGP hold timer in seconds (0 = FRR default)
	BFDTransmitMS uint32 // BFD transmit interval in ms (default: 300)
	BFDReceiveMS  uint32 // BFD receive interval in ms (default: 300)

	// BGP peering mode (GoBGP).
	BGPPeerMode  PeerMode // Unnumbered (default), dual, or numbered
	BGPNeighbors string   // Comma-separated numbered peer IPs
	BGPRemoteASN uint32   // Remote ASN for numbered peers (0 = iBGP)

	// Common fields.
	BridgeName  string // Default: "br.provision"
	VRFName     string // Default: empty (no VRF isolation); set explicitly if needed
	MTU         int    // Default: 9000
	NetworkMode string // "gobgp" to use in-process GoBGP instead of FRR

	// VLAN configuration.
	VLANs []VLANConfig // 802.1Q VLAN interfaces to create before network mode setup
}

// VLANConfig holds configuration for a single 802.1Q VLAN interface.
type VLANConfig struct {
	ID      int    // VLAN identifier (1-4094)
	Parent  string // Physical parent interface (e.g. "eno1")
	Address string // Static IP/CIDR (e.g. "10.200.0.42/24"); empty = DHCP
	Gateway string // Default gateway IP (optional)
}

// ApplyDefaults fills in default values for unset fields.
func (c *Config) ApplyDefaults() {
	if c.BridgeName == "" {
		c.BridgeName = "br.provision"
	}
	// VRFName is intentionally left empty by default — standard EVPN runs
	// the underlay in the default namespace. Set vrf_name explicitly if
	// VRF isolation is required.
	if c.MTU == 0 {
		c.MTU = 9000
	}
	if c.VRFTableID == 0 {
		c.VRFTableID = 1
	}
	// BFD is opt-in: only enabled when bfd_transmit_ms / bfd_receive_ms
	// are explicitly set via environment variables.
}

// IsFRRMode returns true if the config has enough parameters for FRR/EVPN.
func (c *Config) IsFRRMode() bool {
	return (c.UnderlaySubnet != "" || c.UnderlayIP != "") && c.ASN != 0
}

// IsStaticMode returns true if static IP configuration is provided.
func (c *Config) IsStaticMode() bool {
	return c.StaticIP != ""
}

// IsBondMode returns true if LACP bonding interfaces are configured.
func (c *Config) IsBondMode() bool {
	return c.BondInterfaces != ""
}

// IsVLANMode returns true if any VLAN interfaces are configured.
func (c *Config) IsVLANMode() bool {
	return len(c.VLANs) > 0
}

// ParseVLANs parses a comma-separated VLAN specification string into
// a slice of VLANConfig. Each entry has the format:
//
//	ID:parent[:address[:gateway]]
//
// Examples:
//
//	"200:eno1:10.200.0.42/24"
//	"200:eno1:10.200.0.42/24,300:eno2"
//	"200:eno1:10.200.0.42/24:10.200.0.1"
func ParseVLANs(spec string) ([]VLANConfig, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	var configs []VLANConfig
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 4)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid VLAN entry %q: need at least ID:parent", entry)
		}

		id, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid VLAN ID %q: %w", parts[0], err)
		}
		if id < 1 || id > 4094 {
			return nil, fmt.Errorf("VLAN ID %d out of range (1-4094)", id)
		}

		cfg := VLANConfig{
			ID:     id,
			Parent: parts[1],
		}
		if len(parts) >= 3 {
			cfg.Address = parts[2]
		}
		if len(parts) >= 4 {
			cfg.Gateway = parts[3]
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// IsGoBGPMode returns true if GoBGP mode is explicitly requested.
func (c *Config) IsGoBGPMode() bool {
	return c.NetworkMode == "gobgp"
}

// PeerMode controls how BGP neighbor sessions are established.
type PeerMode string

const (
	// PeerModeUnnumbered uses link-local interface peers for both IPv4 unicast
	// and L2VPN-EVPN address families.  This is the default for leaf-peered
	// datacenter fabrics running BGP unnumbered.
	PeerModeUnnumbered PeerMode = "unnumbered"

	// PeerModeDual combines unnumbered interface peers (IPv4 unicast only) with
	// numbered peers for L2VPN-EVPN.  Typical use case: fabric underlay via
	// unnumbered, EVPN via iBGP to route reflectors.
	PeerModeDual PeerMode = "dual"

	// PeerModeNumbered uses explicit neighbor IPs for all BGP sessions.  The
	// machine must already have underlay reachability (e.g. via DHCP or static
	// IP).  All configured address families are negotiated over the numbered
	// sessions.
	PeerModeNumbered PeerMode = "numbered"
)

// ParsePeerMode converts a string to a PeerMode, defaulting to unnumbered.
func ParsePeerMode(s string) PeerMode {
	switch PeerMode(strings.ToLower(s)) {
	case PeerModeDual:
		return PeerModeDual
	case PeerModeNumbered:
		return PeerModeNumbered
	default:
		return PeerModeUnnumbered
	}
}
