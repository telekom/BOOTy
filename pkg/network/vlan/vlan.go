//go:build linux

// Package vlan provides IEEE 802.1Q VLAN interface management for BOOTy.
package vlan

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/vishvananda/netlink"
)

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

// Setup creates a VLAN interface on the given parent and optionally configures
// an address and gateway route. It returns the name of the created interface.
func Setup(cfg Config) (string, error) {
	if cfg.ID < 1 || cfg.ID > 4094 {
		return "", fmt.Errorf("invalid VLAN ID %d: must be 1-4094", cfg.ID)
	}

	parent, err := netlink.LinkByName(cfg.Parent)
	if err != nil {
		return "", fmt.Errorf("parent interface %s not found: %w", cfg.Parent, err)
	}

	// Ensure parent is up.
	if err := netlink.LinkSetUp(parent); err != nil {
		return "", fmt.Errorf("bring up parent %s: %w", cfg.Parent, err)
	}

	vlanName := cfg.InterfaceName()
	vlanLink := &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        vlanName,
			ParentIndex: parent.Attrs().Index,
		},
		VlanId: cfg.ID,
	}

	if err := netlink.LinkAdd(vlanLink); err != nil {
		return "", fmt.Errorf("create VLAN %d on %s: %w", cfg.ID, cfg.Parent, err)
	}

	if err := netlink.LinkSetUp(vlanLink); err != nil {
		return "", fmt.Errorf("bring up VLAN interface %s: %w", vlanName, err)
	}

	if cfg.Address != "" {
		addr, err := netlink.ParseAddr(cfg.Address)
		if err != nil {
			return "", fmt.Errorf("parse VLAN address %q: %w", cfg.Address, err)
		}
		if err := netlink.AddrAdd(vlanLink, addr); err != nil {
			return "", fmt.Errorf("assign address %s to %s: %w", cfg.Address, vlanName, err)
		}
	}

	if cfg.Gateway != "" {
		gw := net.ParseIP(cfg.Gateway)
		if gw == nil {
			return "", fmt.Errorf("invalid gateway IP %q", cfg.Gateway)
		}
		route := &netlink.Route{
			LinkIndex: vlanLink.Attrs().Index,
			Gw:        gw,
		}
		if err := netlink.RouteAdd(route); err != nil {
			return "", fmt.Errorf("add gateway route %s on %s: %w", cfg.Gateway, vlanName, err)
		}
	}

	slog.Info("VLAN interface created", "name", vlanName, "id", cfg.ID, "parent", cfg.Parent)
	return vlanName, nil
}

// Teardown removes a VLAN interface by name. It is safe to call if the
// interface has already been removed.
func Teardown(parentName string, vlanID int) error {
	vlanName := fmt.Sprintf("%s.%d", parentName, vlanID)

	link, err := netlink.LinkByName(vlanName)
	if err != nil {
		// Interface does not exist — nothing to tear down.
		return nil //nolint:nilerr // not-found means already removed
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete VLAN interface %s: %w", vlanName, err)
	}

	slog.Info("VLAN interface removed", "name", vlanName)
	return nil
}

// SetupAll creates all VLAN interfaces from a slice of configs.
// Returns the names of all created interfaces. If any setup fails,
// previously created VLANs are torn down and the error is returned.
func SetupAll(cfgs []Config) ([]string, error) {
	var names []string
	for _, cfg := range cfgs {
		name, err := Setup(cfg)
		if err != nil {
			// Roll back already-created VLANs.
			for _, c := range cfgs[:len(names)] {
				_ = Teardown(c.Parent, c.ID)
			}
			return nil, fmt.Errorf("VLAN setup failed: %w", err)
		}
		names = append(names, name)
	}
	return names, nil
}

// TeardownAll removes all VLAN interfaces. Errors are logged but do not
// stop teardown of remaining interfaces.
func TeardownAll(cfgs []Config) {
	for _, cfg := range cfgs {
		if err := Teardown(cfg.Parent, cfg.ID); err != nil {
			slog.Warn("VLAN teardown error", "vlan", cfg.InterfaceName(), "error", err)
		}
	}
}
