//go:build linux

package network

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

// ResolveLinkMatch on injected NICs: a unique glob or MAC selects one NIC,
// a match of several NICs is an error (the caller then uses the first NIC).
func TestResolveLinkMatchInjectedLinks(t *testing.T) {
	restore := snapshotNICFns()
	defer restore()
	mac := func(s string) net.HardwareAddr {
		hw, _ := net.ParseMAC(s)
		return hw
	}
	dev := func(name, m string) netlink.Link {
		return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name, HardwareAddr: mac(m)}}
	}
	linkList = func() ([]netlink.Link, error) {
		return []netlink.Link{
			dev("lo", "00:00:00:00:00:00"),
			dev("eth0", "02:00:00:00:00:01"),
			dev("enp1s4", "02:00:00:00:00:02"),
			dev("enp2s4", "02:00:00:00:00:03"),
			&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "enp9s4"}}, // no MAC: not a NIC
			// A ContainerLab NIC is a renamed veth.
			&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "ens1f0np0", HardwareAddr: mac("02:00:00:00:00:04")}},
		}, nil
	}
	for _, tt := range []struct {
		m    LinkMatch
		want string
	}{
		{LinkMatch{Name: "enp1*"}, "enp1s4"},
		{LinkMatch{MAC: "02:00:00:00:00:03"}, "enp2s4"},
		{LinkMatch{Name: "enp*", MAC: "02:00:00:00:00:02"}, "enp1s4"},
		{LinkMatch{Name: "ens1f0*"}, "ens1f0np0"},
	} {
		if got, err := ResolveLinkMatch(tt.m); err != nil || got != tt.want {
			t.Errorf("ResolveLinkMatch(%+v) = %q, %v, want %q", tt.m, got, err, tt.want)
		}
	}
	for _, m := range []LinkMatch{{Name: "enp*"}, {Name: "wl*"}} {
		if got, err := ResolveLinkMatch(m); err == nil {
			t.Errorf("ResolveLinkMatch(%+v) = %q, want an error", m, got)
		}
	}
}

// A set-name that BOOTy would not see as a NIC fails before the rename.
func TestApplyLinkRenamesRejectsExcludedNames(t *testing.T) {
	restore := snapshotNICFns()
	defer restore()
	listed := false
	linkList = func() ([]netlink.Link, error) {
		listed = true
		return nil, nil
	}
	for _, name := range []string{"local0", "br-prov", "bond1", "vx.3000"} {
		if err := ApplyLinkRenames([]LinkMatch{{Name: "enp1*", SetName: name}}); err == nil {
			t.Errorf("set-name %q: no error", name)
		}
	}
	if listed {
		t.Fatal("NICs were listed for a rejected set-name")
	}
}
