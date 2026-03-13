// Package gobgp implements a three-tier BGP network stack using the GoBGP
// library as a pure-Go replacement for FRR.
//
// Architecture:
//   - Tier 1 (Underlay): eBGP peering with leaf switches for VXLAN reachability
//   - Tier 2 (Overlay): EVPN Type-5 routes with VXLAN encapsulation
//   - Tier 3 (IPMI): Optional L3 path to the BMC (not yet implemented)
package gobgp

import (
	"context"
	"fmt"
	"time"

	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/network/frr"
)

// Tier represents a single concern in the network stack.
type Tier interface {
	// Setup configures the tier's networking resources.
	Setup(ctx context.Context) error
	// Ready waits for the tier to become operational.
	Ready(ctx context.Context, timeout time.Duration) error
	// Teardown removes the tier's networking resources.
	Teardown(ctx context.Context) error
}

// Config holds GoBGP three-tier stack configuration.
type Config struct {
	ASN               uint32 // Local BGP autonomous system number
	RouterID          string // BGP router ID (underlay IP)
	ListenPort        int32  // BGP listen port (default: 179)
	ProvisionVNI      int    // VXLAN VNI for provisioning network
	ProvisionIP       string // IP/mask for provision bridge
	DNSResolvers      string // Comma-separated DNS servers
	BridgeName        string // Bridge device name (default: "br.provision")
	VRFName           string // VRF name (default: empty, same as FRR)
	VRFTableID        uint32 // Routing table ID for VRF (default: 1000)
	MTU               int    // Physical interface MTU (default: 9000)
	KeepaliveInterval uint64 // BGP keepalive seconds (default: 3)
	HoldTime          uint64 // BGP hold timer seconds (default: 9)
	OverlayIP         string // Overlay loopback IP (derived or same as RouterID)
	BridgeMAC         string // Derived MAC for provision bridge
	IPMIMAC           string // IPMI MAC for bridge MAC derivation
}

// NewConfig creates a GoBGP Config from network configuration.
// It derives addresses using the shared FRR address-derivation logic
// and applies GoBGP-specific defaults (aggressive hold timers, no BFD).
func NewConfig(netCfg *network.Config) (*Config, error) {
	netCfg.ApplyDefaults()

	underlayIP, overlayIP, bridgeMAC, err := frr.DeriveAddresses(netCfg)
	if err != nil {
		return nil, fmt.Errorf("derive addresses: %w", err)
	}

	cfg := &Config{
		ASN:               netCfg.ASN,
		RouterID:          underlayIP,
		ProvisionVNI:      int(netCfg.ProvisionVNI),
		ProvisionIP:       netCfg.ProvisionIP,
		DNSResolvers:      netCfg.DNSResolvers,
		BridgeName:        netCfg.BridgeName,
		VRFName:           netCfg.VRFName,
		VRFTableID:        netCfg.VRFTableID,
		MTU:               netCfg.MTU,
		KeepaliveInterval: uint64(netCfg.BGPKeepalive),
		HoldTime:          uint64(netCfg.BGPHold),
		OverlayIP:         overlayIP,
		BridgeMAC:         bridgeMAC,
		IPMIMAC:           netCfg.IPMIMAC,
	}

	cfg.ApplyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ApplyDefaults fills in default values for unset fields.
// GoBGP uses aggressive hold timers (3s/9s) instead of BFD for fast failover.
func (c *Config) ApplyDefaults() {
	if c.ListenPort == 0 {
		c.ListenPort = 179
	}
	if c.KeepaliveInterval == 0 {
		c.KeepaliveInterval = 3
	}
	if c.HoldTime == 0 {
		c.HoldTime = 9
	}
	if c.BridgeName == "" {
		c.BridgeName = "br.provision"
	}
	if c.MTU == 0 {
		c.MTU = 9000
	}
	if c.VRFTableID == 0 {
		c.VRFTableID = 1000
	}
}

// Validate checks that required configuration fields are present.
func (c *Config) Validate() error {
	if c.ASN == 0 {
		return fmt.Errorf("ASN is required for GoBGP mode")
	}
	if c.RouterID == "" {
		return fmt.Errorf("router ID (underlay IP) is required for GoBGP mode")
	}
	return nil
}
