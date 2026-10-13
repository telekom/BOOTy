//go:build linux

package network

import (
	"context"
	"log/slog"
	"strings"

	"github.com/telekom/BOOTy/pkg/network/netname"
)

// predictableSysfs is the sysfs mount the predictable names are read from.
var predictableSysfs = "/sys"

// ApplyPredictableNames renames NICs to the predictable names (systemd
// net_id) that the network config uses and that do not exist yet, so a
// netplan or frr.conf written for the installed OS (enp1s0) finds the
// ramdisk NIC (eth0). It runs before the netplan set-name renames. When a
// rename fails, the renames done before it are undone.
func ApplyPredictableNames(ctx context.Context, c *Config) error {
	links, err := physicalLinks(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(links))
	for _, l := range links {
		names = append(names, l.Name)
	}
	var done [][2]string // from, to
	for from, to := range netname.Plan(predictableSysfs, names, configuredInterfaceNames(c)) {
		if err := renameLink(from, to); err != nil {
			return undoRenames(done, err)
		}
		done = append(done, [2]string{from, to})
		slog.Info("renamed NIC to its predictable name", "from", from, "to", to)
	}
	return nil
}

// configuredInterfaceNames returns the interface names of the network
// config (netplan, frr.conf and vars).
func configuredInterfaceNames(c *Config) []string {
	names := append([]string{c.StaticIface, c.VXLANLink}, c.Interfaces...)
	names = append(names, strings.Split(c.BondInterfaces, ",")...)
	names = append(names, strings.Split(c.BGPInterfaces, ",")...)
	for _, v := range c.VLANs {
		names = append(names, v.Parent)
	}
	out := names[:0]
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && n != "auto-detect" {
			out = append(out, n)
		}
	}
	return out
}
