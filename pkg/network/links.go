//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/vishvananda/netlink"
)

// LinksMode brings every physical NIC up with an IPv6 link-local address
// only: no DHCP, no other address, no route, no BGP (NETWORK_MODE=links).
// It serves checks that only need the link layer, such as the LLDP cable
// check, on fabric ports where DHCP would hang.
type LinksMode struct{}

// Test seams.
var (
	linksProcSys = "/proc/sys/net/ipv6/conf"
	linksDetect  = DetectPhysicalNICsContext
	linksSetUp   = func(name string) error {
		link, err := netlink.LinkByName(name)
		if err != nil {
			return fmt.Errorf("find %s: %w", name, err)
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("set %s up: %w", name, err)
		}
		return nil
	}
)

// Setup turns off router advertisements and address autoconfiguration on
// every physical NIC, so it keeps only its link-local address, and sets
// the NICs up.
func (m *LinksMode) Setup(ctx context.Context, _ *Config) error {
	nics, err := linksDetect(ctx)
	if err != nil {
		return fmt.Errorf("links mode: %w", err)
	}
	if len(nics) == 0 {
		return errors.New("links mode: no physical NICs")
	}
	for _, nic := range nics {
		for _, key := range []string{"accept_ra", "autoconf"} {
			p := filepath.Join(linksProcSys, nic, key)
			if err := os.WriteFile(p, []byte("0"), 0o644); err != nil { //nolint:gosec // sysctl file
				slog.Warn("links mode: sysctl not set", "path", p, "error", err)
			}
		}
		if err := linksSetUp(nic); err != nil {
			return fmt.Errorf("links mode: %w", err)
		}
	}
	slog.Info("links mode: NICs up with IPv6 link-local only", "nics", nics)
	return nil
}

// WaitForConnectivity does not wait: links mode has no routed network.
func (m *LinksMode) WaitForConnectivity(_ context.Context, target string, _ time.Duration) error {
	slog.Warn("links mode has no routed network, not waiting", "target", RedactHTTPURLForLog(target))
	return nil
}

// Teardown leaves the NICs up; there is nothing to remove.
func (m *LinksMode) Teardown(context.Context) error { return nil }
