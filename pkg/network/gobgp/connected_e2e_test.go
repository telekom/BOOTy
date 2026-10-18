//go:build linux_e2e

package gobgp

import (
	"net"
	"net/netip"
	"os"
	"slices"
	"testing"

	"github.com/vishvananda/netlink"
)

// connectedPrefixes lists the address prefixes of the links in the VRF of
// the bridge (the VRF device included) and none of other VRFs. Needs root.
func TestConnectedPrefixesNetlink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("needs root")
	}
	vrf := &netlink.Vrf{LinkAttrs: netlink.LinkAttrs{Name: "cpvrf"}, Table: 4711}
	other := &netlink.Vrf{LinkAttrs: netlink.LinkAttrs{Name: "cpother"}, Table: 4712}
	br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: "cpbr"}}
	in := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "cpin"}}
	out := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "cpout"}}
	for _, l := range []netlink.Link{vrf, other, br, in, out} {
		_ = netlink.LinkDel(l)
		if err := netlink.LinkAdd(l); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = netlink.LinkDel(l) })
	}
	set := func(name, master, cidr string) {
		t.Helper()
		l, _ := netlink.LinkByName(name)
		m, _ := netlink.LinkByName(master)
		if err := netlink.LinkSetMaster(l, m); err != nil {
			t.Fatal(err)
		}
		ip, n, _ := net.ParseCIDR(cidr)
		n.IP = ip
		if err := netlink.AddrAdd(l, &netlink.Addr{IPNet: n}); err != nil {
			t.Fatal(err)
		}
	}
	set("cpbr", "cpvrf", "10.200.0.10/24")
	set("cpin", "cpvrf", "10.201.0.1/30")
	set("cpout", "cpother", "10.202.0.1/24")
	got := connectedPrefixes("cpbr")
	for _, want := range []string{"10.200.0.0/24", "10.201.0.0/30"} {
		if !slices.Contains(got, netip.MustParsePrefix(want)) {
			t.Errorf("connected prefixes %v, want %s", got, want)
		}
	}
	if slices.Contains(got, netip.MustParsePrefix("10.202.0.0/24")) {
		t.Errorf("connected prefixes %v include the other VRF's", got)
	}
}
