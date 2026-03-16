// Package vlan provides VLAN configuration and validation.
package vlan

import (
	"fmt"
	"net"
	"strings"
)

// MinID is the minimum valid VLAN ID.
const MinID = 1

// MaxID is the maximum valid VLAN ID.
const MaxID = 4094

// Config describes a VLAN interface.
type Config struct {
	ID          int    `json:"id"`
	Parent      string `json:"parent"`
	Name        string `json:"name,omitempty"`
	Address     string `json:"address,omitempty"` // CIDR.
	Gateway     string `json:"gateway,omitempty"`
	MTU         int    `json:"mtu,omitempty"`
	DHCP        bool   `json:"dhcp,omitempty"`
	Description string `json:"description,omitempty"`
}

// InterfaceName returns the VLAN interface name.
func (c *Config) InterfaceName() string {
	if c.Name != "" {
		return c.Name
	}
	return fmt.Sprintf("%s.%d", c.Parent, c.ID)
}

// Validate checks the VLAN config.
func (c *Config) Validate() error {
	if c.ID < MinID || c.ID > MaxID {
		return fmt.Errorf("vlan id %d out of range [%d, %d]", c.ID, MinID, MaxID)
	}
	if c.Parent == "" {
		return fmt.Errorf("parent interface required")
	}
	if c.Address != "" {
		if _, _, err := net.ParseCIDR(c.Address); err != nil {
			return fmt.Errorf("invalid address %q: %w", c.Address, err)
		}
	}
	if c.DHCP && c.Address != "" {
		return fmt.Errorf("dhcp and static address are mutually exclusive")
	}
	if c.Gateway != "" {
		if net.ParseIP(c.Gateway) == nil {
			return fmt.Errorf("invalid gateway %q", c.Gateway)
		}
	}
	if c.MTU < 0 || c.MTU > 9216 {
		return fmt.Errorf("mtu %d out of range [0, 9216]", c.MTU)
	}
	return nil
}

// TrunkConfig describes a VLAN trunk (multiple VLANs on one port).
type TrunkConfig struct {
	Parent   string `json:"parent"`
	VLANs    []int  `json:"vlans"`
	NativeID int    `json:"nativeId,omitempty"`
	AllowAll bool   `json:"allowAll,omitempty"`
}

// Validate checks the trunk config.
func (c *TrunkConfig) Validate() error {
	if c.Parent == "" {
		return fmt.Errorf("parent interface required")
	}
	if !c.AllowAll && len(c.VLANs) == 0 {
		return fmt.Errorf("at least one VLAN required when allowAll is false")
	}
	seen := make(map[int]bool)
	for _, id := range c.VLANs {
		if id < MinID || id > MaxID {
			return fmt.Errorf("vlan id %d out of range", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate vlan id %d", id)
		}
		seen[id] = true
	}
	if c.NativeID != 0 && (c.NativeID < MinID || c.NativeID > MaxID) {
		return fmt.Errorf("native vlan id %d out of range", c.NativeID)
	}
	if c.NativeID != 0 && !c.AllowAll && !seen[c.NativeID] {
		return fmt.Errorf("native vlan id %d must be present in vlan list when allowAll is false", c.NativeID)
	}
	return nil
}

// MultiConfig holds multiple VLAN configs.
type MultiConfig struct {
	VLANs []Config `json:"vlans"`
}

// Validate checks all VLAN configs for validity and uniqueness.
func (m *MultiConfig) Validate() error {
	seen := make(map[string]bool)
	seenNames := make(map[string]bool)
	for i := range m.VLANs {
		if err := m.VLANs[i].Validate(); err != nil {
			return fmt.Errorf("vlan %d: %w", i, err)
		}
		key := fmt.Sprintf("%s:%d", m.VLANs[i].Parent, m.VLANs[i].ID)
		if seen[key] {
			return fmt.Errorf("duplicate vlan %s.%d", m.VLANs[i].Parent, m.VLANs[i].ID)
		}
		seen[key] = true

		ifName := m.VLANs[i].InterfaceName()
		if seenNames[ifName] {
			return fmt.Errorf("duplicate vlan interface name %q", ifName)
		}
		seenNames[ifName] = true
	}
	return nil
}

// Names returns all VLAN interface names.
func (m *MultiConfig) Names() []string {
	names := make([]string, 0, len(m.VLANs))
	for i := range m.VLANs {
		names = append(names, m.VLANs[i].InterfaceName())
	}
	return names
}

// FormatVLANList formats a list of VLAN IDs for display.
func FormatVLANList(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return strings.Join(parts, ", ")
}
