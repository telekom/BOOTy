// Package vlan provides IEEE 802.1Q VLAN interface management for BOOTy.
package vlan

import "fmt"

// Config holds configuration for a single VLAN interface.
type Config struct {
	// ID is the 802.1Q VLAN identifier (1-4094).
	ID int
	// Parent is the name of the physical interface (e.g. "eno1").
	Parent string
	// Address is the static IP/CIDR to assign (e.g. "10.200.0.42/24").
	// When empty, the VLAN interface is left unconfigured for DHCP.
	Address string
	// Gateway is the default gateway IP for this VLAN (optional).
	Gateway string
}

// InterfaceName returns the conventional VLAN interface name (e.g. "eno1.200").
func (c *Config) InterfaceName() string {
	return fmt.Sprintf("%s.%d", c.Parent, c.ID)
}
