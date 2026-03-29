// Package vrf provides VRF (Virtual Routing and Forwarding) configuration types
// and validation for multi-VRF network isolation.
package vrf

import (
	"fmt"
	"math"
)

// Config defines a VRF instance.
type Config struct {
	Name    string   `json:"name"`
	TableID uint32   `json:"tableId"`
	Members []string `json:"members"` // interfaces to assign.
}

// MultiVRFConfig holds multiple VRF definitions.
type MultiVRFConfig struct {
	Enabled      bool     `json:"enabled,omitempty"`
	Management   *Config  `json:"management,omitempty"`
	Provisioning *Config  `json:"provisioning,omitempty"`
	Extra        []Config `json:"extra,omitempty"`
}

// maxTableID is the maximum valid routing table ID for VRF on Linux.
const maxTableID = math.MaxUint32 - 1

// Validate checks the VRF configuration for correctness.
func (c *Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("vrf name is required")
	}
	if c.TableID == 0 {
		return fmt.Errorf("vrf %s: table ID must be > 0", c.Name)
	}
	// Linux kernel reserves table IDs 253 (default), 254 (main), 255 (local).
	if c.TableID >= 253 && c.TableID <= 255 {
		return fmt.Errorf("vrf %s: table ID %d is reserved by the Linux kernel", c.Name, c.TableID)
	}
	if c.TableID > maxTableID {
		return fmt.Errorf("vrf %s: table ID %d exceeds maximum", c.Name, c.TableID)
	}
	return nil
}

// AllConfigs returns all VRF configurations as a flat list.
func (m *MultiVRFConfig) AllConfigs() []Config {
	if m == nil || !m.Enabled {
		return nil
	}
	var out []Config
	if m.Management != nil {
		out = append(out, *m.Management)
	}
	if m.Provisioning != nil {
		out = append(out, *m.Provisioning)
	}
	out = append(out, m.Extra...)
	return out
}

// ValidateAll checks all VRF configs for name/table conflicts.
func (m *MultiVRFConfig) ValidateAll() error {
	configs := m.AllConfigs()
	names := make(map[string]bool, len(configs))
	tables := make(map[uint32]bool, len(configs))

	for _, cfg := range configs {
		if err := cfg.Validate(); err != nil {
			return err
		}
		if names[cfg.Name] {
			return fmt.Errorf("duplicate VRF name: %s", cfg.Name)
		}
		names[cfg.Name] = true
		if tables[cfg.TableID] {
			return fmt.Errorf("duplicate VRF table ID: %d", cfg.TableID)
		}
		tables[cfg.TableID] = true
	}
	return nil
}
