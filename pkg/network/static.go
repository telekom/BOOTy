//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
)

// StaticMode implements the Mode interface using static IP configuration.
type StaticMode struct {
	iface   string
	address *netlink.Addr
	gateway net.IP
}

// Setup configures static IP, gateway, and DNS on the specified interface.
func (s *StaticMode) Setup(_ context.Context, cfg *Config) error {
	if cfg.StaticIP == "" {
		return fmt.Errorf("static mode requires StaticIP")
	}

	addr, err := netlink.ParseAddr(cfg.StaticIP)
	if err != nil {
		return fmt.Errorf("parsing static IP %q: %w", cfg.StaticIP, err)
	}
	s.address = addr

	if cfg.StaticGateway != "" {
		s.gateway = net.ParseIP(cfg.StaticGateway)
		if s.gateway == nil {
			return fmt.Errorf("invalid gateway IP %q", cfg.StaticGateway)
		}
	}

	// Find the target interface.
	ifaceName := cfg.StaticIface
	if ifaceName == "" {
		// Auto-detect first physical NIC.
		nics, err := DetectPhysicalNICs()
		if err != nil || len(nics) == 0 {
			return fmt.Errorf("no physical NICs found for static mode")
		}
		ifaceName = nics[0]
	}
	s.iface = ifaceName

	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return fmt.Errorf("finding interface %s: %w", ifaceName, err)
	}

	// Bring the interface up.
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing up %s: %w", ifaceName, err)
	}

	// Assign the static IP.
	if err := netlink.AddrAdd(link, addr); err != nil {
		if !errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("assigning address to %s: %w", ifaceName, err)
		}
		slog.Info("address already assigned", "iface", ifaceName, "addr", addr)
	}
	slog.Info("static IP configured", "iface", ifaceName, "address", cfg.StaticIP)

	// Add default route via gateway.
	if s.gateway != nil {
		route := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Gw:        s.gateway,
		}
		if err := netlink.RouteAdd(route); err != nil {
			if !errors.Is(err, syscall.EEXIST) {
				return fmt.Errorf("adding default route via %s: %w", s.gateway, err)
			}
			slog.Info("default route already exists", "gateway", s.gateway)
		}
		slog.Info("default route configured", "gateway", cfg.StaticGateway)
	}

	return nil
}

// WaitForConnectivity polls the target URL until reachable or timeout.
func (s *StaticMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

// Teardown removes the static IP configuration.
func (s *StaticMode) Teardown(_ context.Context) error {
	if s.iface == "" {
		return nil
	}
	link, err := netlink.LinkByName(s.iface)
	if err != nil {
		return nil //nolint:nilerr // interface may already be gone
	}
	// Remove default route before address to avoid routing table inconsistency.
	if s.gateway != nil {
		route := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Gw:        s.gateway,
		}
		if err := netlink.RouteDel(route); err != nil {
			slog.Debug("route deletion (may already be gone)", "gateway", s.gateway, "error", err)
		}
	}
	if s.address != nil {
		_ = netlink.AddrDel(link, s.address)
	}
	slog.Info("Static network teardown complete", "iface", s.iface)
	return nil
}
