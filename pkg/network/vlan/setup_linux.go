//go:build linux

package vlan

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/vishvananda/netlink"
)

var (
	linkByName = netlink.LinkByName
	linkSetUp  = netlink.LinkSetUp
	linkSetMTU = netlink.LinkSetMTU
	linkAdd    = netlink.LinkAdd
	linkDel    = netlink.LinkDel
	parseAddr  = netlink.ParseAddr
	addrAdd    = netlink.AddrAdd
	routeAdd   = netlink.RouteAdd
)

// Setup creates a VLAN interface on the given parent and optionally configures
// an address and gateway route. It returns the name of the created interface.
func Setup(cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("vlan config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return "", fmt.Errorf("validate vlan config: %w", err)
	}

	parent, err := linkByName(cfg.Parent)
	if err != nil {
		return "", fmt.Errorf("parent interface %s not found: %w", cfg.Parent, err)
	}

	if err := linkSetUp(parent); err != nil {
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

	if err := linkAdd(vlanLink); err != nil {
		return "", fmt.Errorf("create VLAN %d on %s: %w", cfg.ID, cfg.Parent, err)
	}

	if cfg.MTU > 0 {
		if err := linkSetMTU(vlanLink, cfg.MTU); err != nil {
			return "", fmt.Errorf("set mtu %d on %s: %w", cfg.MTU, vlanName, err)
		}
	}

	if err := linkSetUp(vlanLink); err != nil {
		return "", fmt.Errorf("bring up VLAN interface %s: %w", vlanName, err)
	}

	if cfg.Address != "" {
		addr, err := parseAddr(cfg.Address)
		if err != nil {
			return "", fmt.Errorf("parse VLAN address %q: %w", cfg.Address, err)
		}
		if err := addrAdd(vlanLink, addr); err != nil {
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
		if err := routeAdd(route); err != nil {
			return "", fmt.Errorf("add gateway route %s on %s: %w", cfg.Gateway, vlanName, err)
		}
	}

	slog.Info("VLAN interface created", "name", vlanName, "id", cfg.ID, "parent", cfg.Parent)
	return vlanName, nil
}

// Teardown removes a VLAN interface derived from parentName and vlanID.
// It is safe to call if the interface has already been removed.
func Teardown(parentName string, vlanID int) error {
	vlanName := fmt.Sprintf("%s.%d", parentName, vlanID)
	return teardownByName(vlanName)
}

func teardownByName(vlanName string) error {
	link, err := linkByName(vlanName)
	if err != nil {
		return nil //nolint:nilerr // not-found means already removed
	}

	if err := linkDel(link); err != nil {
		return fmt.Errorf("delete VLAN interface %s: %w", vlanName, err)
	}

	slog.Info("VLAN interface removed", "name", vlanName)
	return nil
}

// SetupAll creates all VLAN interfaces from a slice of configs.
func SetupAll(cfgs []Config) ([]string, error) {
	var names []string
	for i := range cfgs {
		name, err := Setup(&cfgs[i])
		if err != nil {
			for _, c := range cfgs[:len(names)] {
				_ = teardownByName(c.InterfaceName())
			}
			return nil, fmt.Errorf("VLAN setup failed: %w", err)
		}
		names = append(names, name)
	}
	return names, nil
}

// TeardownAll removes all VLAN interfaces.
func TeardownAll(cfgs []Config) {
	for _, cfg := range cfgs {
		if err := teardownByName(cfg.InterfaceName()); err != nil {
			slog.Warn("VLAN teardown error", "vlan", cfg.InterfaceName(), "error", err)
		}
	}
}
