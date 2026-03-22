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
	cleanup := func() {
		if err := teardownByName(vlanName); err != nil {
			slog.Warn("failed to cleanup partially configured vlan", "vlan", vlanName, "error", err)
		}
	}

	if err := applyVLANMTU(cfg, vlanLink, vlanName, cleanup); err != nil {
		return "", err
	}

	if err := linkSetUp(vlanLink); err != nil {
		cleanup()
		return "", fmt.Errorf("bring up VLAN interface %s: %w", vlanName, err)
	}

	if err := applyVLANAddress(cfg, vlanLink, vlanName, cleanup); err != nil {
		return "", err
	}

	if err := applyVLANGateway(cfg, vlanLink, vlanName, cleanup); err != nil {
		return "", err
	}

	slog.Info("vlan interface created", "name", vlanName, "id", cfg.ID, "parent", cfg.Parent)
	return vlanName, nil
}

func applyVLANMTU(cfg *Config, vlanLink netlink.Link, vlanName string, cleanup func()) error {
	if cfg.MTU <= 0 {
		return nil
	}
	if err := linkSetMTU(vlanLink, cfg.MTU); err != nil {
		cleanup()
		return fmt.Errorf("set mtu %d on %s: %w", cfg.MTU, vlanName, err)
	}
	return nil
}

func applyVLANAddress(cfg *Config, vlanLink netlink.Link, vlanName string, cleanup func()) error {
	if cfg.Address == "" {
		return nil
	}
	addr, err := parseAddr(cfg.Address)
	if err != nil {
		cleanup()
		return fmt.Errorf("parse VLAN address %q: %w", cfg.Address, err)
	}
	if err := addrAdd(vlanLink, addr); err != nil {
		cleanup()
		return fmt.Errorf("assign address %s to %s: %w", cfg.Address, vlanName, err)
	}
	return nil
}

func applyVLANGateway(cfg *Config, vlanLink netlink.Link, vlanName string, cleanup func()) error {
	if cfg.Gateway == "" {
		return nil
	}
	gw := net.ParseIP(cfg.Gateway)
	if gw == nil {
		return fmt.Errorf("invalid gateway IP %q", cfg.Gateway)
	}
	route := &netlink.Route{
		LinkIndex: vlanLink.Attrs().Index,
		Gw:        gw,
	}
	if err := routeAdd(route); err != nil {
		cleanup()
		return fmt.Errorf("add gateway route %s on %s: %w", cfg.Gateway, vlanName, err)
	}
	return nil
}

// Teardown removes a VLAN interface derived from parentName and vlanID.
// It is safe to call if the interface has already been removed.
func Teardown(parentName string, vlanID int) error {
	vlanName := fmt.Sprintf("%s.%d", parentName, vlanID)
	return teardownByName(vlanName)
}

// TeardownConfig removes a VLAN interface based on its effective interface name.
func TeardownConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("vlan config is nil")
	}
	return teardownByName(cfg.InterfaceName())
}

func teardownByName(vlanName string) error {
	link, err := linkByName(vlanName)
	if err != nil {
		return nil //nolint:nilerr // not-found means already removed
	}

	if err := linkDel(link); err != nil {
		return fmt.Errorf("delete VLAN interface %s: %w", vlanName, err)
	}

	slog.Info("vlan interface removed", "name", vlanName)
	return nil
}

// SetupAll creates all VLAN interfaces from a slice of configs.
func SetupAll(cfgs []Config) ([]string, error) {
	var names []string
	for i := range cfgs {
		name, err := Setup(&cfgs[i])
		if err != nil {
			for j := range cfgs[:len(names)] {
				_ = TeardownConfig(&cfgs[j])
			}
			return nil, fmt.Errorf("vlan setup failed: %w", err)
		}
		names = append(names, name)
	}
	return names, nil
}

// TeardownAll removes all VLAN interfaces.
func TeardownAll(cfgs []Config) {
	for i := range cfgs {
		if err := TeardownConfig(&cfgs[i]); err != nil {
			slog.Warn("vlan teardown error", "vlan", cfgs[i].InterfaceName(), "error", err)
		}
	}
}
