//go:build linux

package gobgp

import (
	"fmt"
	"net"
	"os"

	"github.com/vishvananda/netlink"
)

// This file applies the netplan device settings (systemd-networkd
// semantics) of the provision VXLAN device and bridge, the NIC accept-ra
// settings and the forwarding sysctls.

// addrGenModeNone is IN6_ADDR_GEN_MODE_NONE: no IPv6 link-local address,
// like networkd LinkLocalAddressing=no (netplan "link-local: []").
const addrGenModeNone = 1

// vxlanDeviceMTU is the netplan tunnel MTU, else the NIC MTU minus the
// VXLAN overhead.
func (o *OverlayTier) vxlanDeviceMTU() int {
	if o.cfg.OverlayDevices.VXLANMTU > 0 {
		return o.cfg.OverlayDevices.VXLANMTU
	}
	return o.vxlanMTU()
}

// applyVXLANSettings sets the netplan MAC, learning and ARP proxy of a
// VXLAN device that is not created yet.
func (o *OverlayTier) applyVXLANSettings(v *netlink.Vxlan) error {
	d := &o.cfg.OverlayDevices
	if d.VXLANMAC != "" {
		mac, err := net.ParseMAC(d.VXLANMAC)
		if err != nil {
			return fmt.Errorf("parse VXLAN MAC %s: %w", d.VXLANMAC, err)
		}
		v.HardwareAddr = mac
	}
	v.Learning = boolValue(d.VXLANLearning)
	v.Proxy = boolValue(d.VXLANARPProxy)
	return nil
}

// applyLinkSettings sets the IPv6 link-local mode and accept_ra of a link
// before it goes up.
func applyLinkSettings(link netlink.Link, linkLocal, acceptRA *bool) error {
	if linkLocal != nil && !*linkLocal {
		if err := netlink.LinkSetIP6AddrGenMode(link, addrGenModeNone); err != nil {
			return fmt.Errorf("disable IPv6 link-local on %s: %w", link.Attrs().Name, err)
		}
	}
	if acceptRA != nil {
		return setAcceptRA(link.Attrs().Name, *acceptRA)
	}
	return nil
}

// applyPortSettings sets the netplan hairpin and port-mac-learning of the
// VXLAN device as bridge port.
func (o *OverlayTier) applyPortSettings(port netlink.Link) error {
	d := &o.cfg.OverlayDevices
	if d.PortHairpin != nil {
		if err := netlink.LinkSetHairpin(port, *d.PortHairpin); err != nil {
			return fmt.Errorf("set hairpin on %s: %w", port.Attrs().Name, err)
		}
	}
	if d.PortLearning != nil {
		if err := netlink.LinkSetLearning(port, *d.PortLearning); err != nil {
			return fmt.Errorf("set learning on %s: %w", port.Attrs().Name, err)
		}
	}
	return nil
}

func setAcceptRA(dev string, on bool) error {
	val := "0"
	if on {
		val = "1"
	}
	p := "/proc/sys/net/ipv6/conf/" + dev + "/accept_ra"
	if err := os.WriteFile(p, []byte(val), 0o644); err != nil { //nolint:gosec // sysctl path
		return fmt.Errorf("set %s: %w", p, err)
	}
	return nil
}

func boolValue(b *bool) bool { return b != nil && *b }

// sysctlBool is "1" unless b is set to false.
func sysctlBool(b *bool) string {
	if b != nil && !*b {
		return "0"
	}
	return "1"
}
