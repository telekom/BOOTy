//go:build linux

package network

import (
	"fmt"
	"log/slog"

	"github.com/vishvananda/netlink"
)

// physicalLinks returns the physical NICs, the links of netlink type
// "device" except loopback.
func physicalLinks() ([]LinkCandidate, error) {
	links, err := linkList()
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	var out []LinkCandidate
	for _, l := range links {
		attrs := l.Attrs()
		if l.Type() != "device" || attrs.Name == "lo" {
			continue
		}
		out = append(out, LinkCandidate{Name: attrs.Name, MAC: attrs.HardwareAddr.String()})
	}
	return out, nil
}

// ApplyLinkRenames renames NICs like netplan set-name. Like netplan, a
// rename needs exactly one matching NIC; other cases are logged and skipped.
func ApplyLinkRenames(renames []LinkMatch) error {
	for _, r := range renames {
		links, err := physicalLinks()
		if err != nil {
			return err
		}
		names := r.Matches(links)
		if len(names) != 1 {
			slog.Warn("netplan set-name needs exactly one matching NIC, skipping", "match_name", r.Name, "match_mac", r.MAC, "set_name", r.SetName, "matches", names)
			continue
		}
		if names[0] == r.SetName {
			continue
		}
		if err := renameLink(names[0], r.SetName); err != nil {
			return err
		}
		slog.Info("renamed NIC like netplan set-name", "from", names[0], "to", r.SetName)
	}
	return nil
}

func renameLink(from, to string) error {
	link, err := netlink.LinkByName(from)
	if err != nil {
		return fmt.Errorf("find %s: %w", from, err)
	}
	// The kernel renames only links that are down.
	if err := netlink.LinkSetDown(link); err != nil {
		return fmt.Errorf("set %s down: %w", from, err)
	}
	if err := netlink.LinkSetName(link, to); err != nil {
		return fmt.Errorf("rename %s to %s: %w", from, to, err)
	}
	return nil
}

// ResolveLinkMatch returns the single physical NIC that m selects.
func ResolveLinkMatch(m LinkMatch) (string, error) {
	links, err := physicalLinks()
	if err != nil {
		return "", err
	}
	names := m.Matches(links)
	if len(names) != 1 {
		return "", fmt.Errorf("match name %q mac %q selects %d NICs %v, want 1", m.Name, m.MAC, len(names), names)
	}
	return names[0], nil
}
