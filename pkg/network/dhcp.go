package network

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/vishvananda/netlink"
)

// DHCPMode implements the Mode interface using DHCP on all physical interfaces.
type DHCPMode struct {
	client *dhclient.Client
}

// Setup tries DHCP on each physical interface until one gets a lease.
func (d *DHCPMode) Setup(ctx context.Context, _ *Config) error {
	ifaces, err := physicalInterfaces()
	if err != nil {
		return fmt.Errorf("listing interfaces: %w", err)
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no physical interfaces found for DHCP")
	}

	slog.Info("Trying DHCP on all interfaces", "count", len(ifaces))

	for _, iface := range ifaces {
		slog.Info("Attempting DHCP", "interface", iface.Name)

		link, err := netlink.LinkByName(iface.Name)
		if err != nil {
			slog.Warn("Cannot find link for DHCP", "interface", iface.Name, "error", err)
			continue
		}
		if err := netlink.LinkSetUp(link); err != nil {
			slog.Warn("Cannot bring up link for DHCP", "interface", iface.Name, "error", err)
			continue
		}

		// Short DHCP attempt with timeout.
		leased := make(chan struct{}, 1)
		client := dhclient.Client{
			Iface: &iface,
			OnBound: func(lease *dhclient.Lease) {
				cidr := net.IPNet{IP: lease.FixedAddress, Mask: lease.Netmask}
				addr, _ := netlink.ParseAddr(cidr.String())
				if err := netlink.AddrAdd(link, addr); err != nil {
					slog.Warn("Failed to assign DHCP address", "iface", iface.Name, "error", err)
				} else {
					slog.Info("DHCP lease obtained", "iface", iface.Name, "addr", cidr.String())
				}
				// Default gateway.
				if lease.ServerID != nil {
					_ = netlink.RouteAdd(&netlink.Route{Gw: lease.ServerID})
				}
				select {
				case leased <- struct{}{}:
				default:
				}
			},
		}

		hostname, _ := os.Hostname()
		if hostname != "" {
			client.AddOption(0x0c, []byte(hostname)) // DHCPOptHostname
		}
		client.Start()

		// Wait up to 15 seconds for lease on this interface.
		timer := time.NewTimer(15 * time.Second)
		select {
		case <-leased:
			timer.Stop()
			d.client = &client
			slog.Info("DHCP succeeded", "interface", iface.Name)
			return nil
		case <-timer.C:
			client.Stop()
			slog.Info("DHCP timeout on interface, trying next", "interface", iface.Name)
		case <-ctx.Done():
			client.Stop()
			return fmt.Errorf("context canceled during DHCP: %w", ctx.Err())
		}
	}

	return fmt.Errorf("DHCP failed on all %d interfaces", len(ifaces))
}

// WaitForConnectivity polls the target URL until reachable or timeout.
func (d *DHCPMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

// Teardown stops the DHCP client.
func (d *DHCPMode) Teardown(_ context.Context) error {
	if d.client != nil {
		d.client.Stop()
	}
	return nil
}

// physicalInterfaces returns non-loopback, non-virtual interfaces.
func physicalInterfaces() ([]net.Interface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}
	var result []net.Interface
	for _, i := range all {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip virtual interfaces (veth, docker, bridge, bond slaves).
		if strings.HasPrefix(i.Name, "veth") ||
			strings.HasPrefix(i.Name, "docker") ||
			strings.HasPrefix(i.Name, "br-") {
			continue
		}
		result = append(result, i)
	}
	return result, nil
}

// WaitForHTTP polls target with HTTP HEAD until reachable.
func WaitForHTTP(ctx context.Context, target string, timeout time.Duration) error {
	if target == "" {
		return fmt.Errorf("empty connectivity target URL")
	}

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, http.NoBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req) //nolint:gosec // target is from trusted config
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				slog.Info("Network connectivity established", "target", target, "attempt", attempt)
				return nil
			}
			slog.Debug("Connectivity check: server not ready", "target", target, "status", resp.StatusCode)
		}

		slog.Debug("Connectivity check failed", "target", target, "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}

	return fmt.Errorf("network connectivity timeout after %s (%d attempts)", timeout, attempt)
}
