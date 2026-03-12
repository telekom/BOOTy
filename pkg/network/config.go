// Package network provides network mode abstractions for provisioning.
package network

import (
	"context"
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

	// Common fields.
	BridgeName string // Default: "br.provision"
	VRFName    string // Default: "Vrf_underlay"
	MTU        int    // Default: 9000
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
