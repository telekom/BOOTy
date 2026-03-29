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

// Setup creates a bond interface from the configured interfaces.
func (b *BondMode) Setup(_ context.Context, cfg *Config) error {
	if cfg.BondInterfaces == "" {
		return fmt.Errorf("bond mode requires BondInterfaces")
	}

	mode := netlink.BOND_MODE_802_3AD // default: LACP
	if cfg.BondMode != "" {
		switch strings.ToLower(cfg.BondMode) {
		case "802.3ad", "lacp":
			mode = netlink.BOND_MODE_802_3AD
		case "balance-rr":
			mode = netlink.BOND_MODE_BALANCE_RR
		case "active-backup":
			mode = netlink.BOND_MODE_ACTIVE_BACKUP
		case "balance-xor":
			mode = netlink.BOND_MODE_BALANCE_XOR
		default:
			slog.Warn("Unknown bond mode, using 802.3ad", "mode", cfg.BondMode)
		}
	}

	bond := netlink.NewLinkBond(netlink.LinkAttrs{Name: "bond0"})
	bond.Mode = mode
	bond.Miimon = 100 // 100 ms link monitoring interval for failure detection
	bond.LacpRate = netlink.BOND_LACP_RATE_FAST
	bond.XmitHashPolicy = netlink.BOND_XMIT_HASH_POLICY_LAYER3_4

	if err := netlink.LinkAdd(bond); err != nil {
		if !errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("creating bond0: %w", err)
		}
		slog.Info("bond interface already exists, reusing", "name", "bond0")
	} else {
		slog.Info("created bond interface", "name", "bond0", "mode", mode)
	}

	// Add slave interfaces.
	for _, ifName := range strings.Split(cfg.BondInterfaces, ",") {
		ifName = strings.TrimSpace(ifName)
		if ifName == "" {
			continue
		}
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			slog.Warn("Slave interface not found", "name", ifName, "error", err)
			continue
		}
		// Interface must be down to be enslaved.
		if err := netlink.LinkSetDown(link); err != nil {
			slog.Warn("Failed to bring down slave", "name", ifName, "error", err)
		}
		if err := netlink.LinkSetBondSlave(link, &netlink.Bond{LinkAttrs: *bond.Attrs()}); err != nil {
			slog.Warn("Failed to enslave interface", "name", ifName, "error", err)
			continue
		}
		slog.Info("Enslaved interface to bond", "slave", ifName)
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
			slog.Warn("Failed to remove bond", "error", err)
		}
	}
	return nil
}
