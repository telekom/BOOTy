//go:build linux

package vlan

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/vishvananda/netlink"
)

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

	// Clean up the link if any subsequent configuration step fails.
	cleanup := func() {
		if link, lerr := netlink.LinkByName(vlanName); lerr == nil {
			_ = netlink.LinkDel(link)
		}
	}

	if err := netlink.LinkSetUp(vlanLink); err != nil {
		cleanup()
		return "", fmt.Errorf("bring up VLAN interface %s: %w", vlanName, err)
	}

	if cfg.Address != "" {
		addr, err := netlink.ParseAddr(cfg.Address)
		if err != nil {
			cleanup()
			return "", fmt.Errorf("parse VLAN address %q: %w", cfg.Address, err)
		}
		if err := netlink.AddrAdd(vlanLink, addr); err != nil {
			cleanup()
			return "", fmt.Errorf("assign address %s to %s: %w", cfg.Address, vlanName, err)
		}
	}

	if cfg.Gateway != "" {
		gw := net.ParseIP(cfg.Gateway)
		if gw == nil {
			cleanup()
			return "", fmt.Errorf("invalid gateway IP %q", cfg.Gateway)
		}
		route := &netlink.Route{
			LinkIndex: vlanLink.Attrs().Index,
			Gw:        gw,
		}
		if err := netlink.RouteAdd(route); err != nil {
			cleanup()
			return "", fmt.Errorf("add gateway route %s on %s: %w", cfg.Gateway, vlanName, err)
		}
	}

	slog.Info("vlan interface created", "name", vlanName, "id", cfg.ID, "parent", cfg.Parent)
	return vlanName, nil
}

// Teardown removes a VLAN interface derived from parentName and vlanID.
// It is safe to call if the interface has already been removed.
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

	slog.Info("vlan interface removed", "name", vlanName)
	return nil
}

// TeardownConfig removes the VLAN interface identified by cfg.InterfaceName().
func TeardownConfig(cfg *Config) error {
	vlanName := cfg.InterfaceName()
	link, err := netlink.LinkByName(vlanName)
	if err != nil {
		return nil //nolint:nilerr // not-found means already removed
	}
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete VLAN interface %s: %w", vlanName, err)
	}
	slog.Info("vlan interface removed", "name", vlanName)
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
			for i := range cfgs[:len(names)] {
				_ = TeardownConfig(&cfgs[i])
			}
			return nil, fmt.Errorf("vlan setup failed: %w", err)
		}
		names = append(names, name)
	}
	return names, nil
}

// TeardownAll removes all VLAN interfaces. Errors are logged but do not
// stop teardown of remaining interfaces.
func TeardownAll(cfgs []Config) {
	for i := range cfgs {
		if err := TeardownConfig(&cfgs[i]); err != nil {
			slog.Warn("vlan teardown error", "vlan", cfgs[i].InterfaceName(), "error", err)
		}
	}
}
