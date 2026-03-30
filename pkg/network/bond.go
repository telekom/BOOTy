//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
)

// BondMode implements the Mode interface by creating an LACP bond from
// multiple physical NICs. After Setup the bond interface is available
// as "bond0" for other modes (static/DHCP) to use.
type BondMode struct {
	bond netlink.Link
}

// parseBondMode converts a bond mode string to a netlink constant.
func parseBondMode(mode string) netlink.BondMode {
	switch strings.ToLower(mode) {
	case "802.3ad", "lacp", "":
		return netlink.BOND_MODE_802_3AD
	case "balance-rr":
		return netlink.BOND_MODE_BALANCE_RR
	case "active-backup":
		return netlink.BOND_MODE_ACTIVE_BACKUP
	case "balance-xor":
		return netlink.BOND_MODE_BALANCE_XOR
	default:
		slog.Warn("unknown bond mode, using 802.3ad", "mode", mode)
		return netlink.BOND_MODE_802_3AD
	}
}

// Setup creates a bond interface from the configured interfaces.
func (b *BondMode) Setup(_ context.Context, cfg *Config) error {
	if cfg.BondInterfaces == "" {
		return fmt.Errorf("bond mode requires BondInterfaces")
	}

	bond := netlink.NewLinkBond(netlink.LinkAttrs{Name: "bond0"})
	bond.Mode = parseBondMode(cfg.BondMode)
	bond.Miimon = 100 // 100 ms link monitoring interval for failure detection
	bond.LacpRate = netlink.BOND_LACP_RATE_FAST
	bond.XmitHashPolicy = netlink.BOND_XMIT_HASH_POLICY_LAYER3_4

	if err := netlink.LinkAdd(bond); err != nil {
		if !errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("creating bond0: %w", err)
		}
		slog.Info("bond interface already exists, reusing", "name", "bond0")
	} else {
		slog.Info("created bond interface", "name", "bond0", "mode", bond.Mode)
	}

	// Add slave interfaces.
	enslaved := 0
	for _, ifName := range strings.Split(cfg.BondInterfaces, ",") {
		ifName = strings.TrimSpace(ifName)
		if ifName == "" {
			continue
		}
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			slog.Warn("slave interface not found", "name", ifName, "error", err)
			continue
		}
		// Interface must be down to be enslaved.
		if err := netlink.LinkSetDown(link); err != nil {
			slog.Warn("failed to bring down slave", "name", ifName, "error", err)
		}
		if err := netlink.LinkSetBondSlave(link, &netlink.Bond{LinkAttrs: *bond.Attrs()}); err != nil {
			slog.Warn("failed to enslave interface", "name", ifName, "error", err)
			continue
		}
		slog.Info("enslaved interface to bond", "slave", ifName)
		enslaved++
	}

	if enslaved == 0 {
		return fmt.Errorf("no interfaces were enslaved to bond0")
	}

	// Bring the bond up.
	if err := netlink.LinkSetUp(bond); err != nil {
		return fmt.Errorf("bringing up bond0: %w", err)
	}

	b.bond = bond
	return nil
}

// WaitForConnectivity polls the target URL until reachable or timeout.
func (b *BondMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

// Teardown removes the bond interface.
func (b *BondMode) Teardown(_ context.Context) error {
	if b.bond != nil {
		if err := netlink.LinkDel(b.bond); err != nil {
			slog.Warn("failed to remove bond", "error", err)
		}
	}
	return nil
}
