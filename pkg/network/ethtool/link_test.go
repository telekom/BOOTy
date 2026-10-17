//go:build linux

package ethtool

import (
	"errors"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// reply serializes attributes like a kernel reply after the genl header.
func reply(attrs ...*nl.RtAttr) []byte {
	var b []byte
	for _, a := range attrs {
		b = append(b, a.Serialize()...)
	}
	return b
}

func u64s(v ...uint64) []byte {
	b := make([]byte, 0, 8*len(v))
	for _, x := range v {
		b = append(b, nl.Uint64Attr(x)...)
	}
	return b
}

func TestParseLinkModes(t *testing.T) {
	m, err := parseLinkModes(reply(
		nl.NewRtAttr(unix.ETHTOOL_A_LINKMODES_AUTONEG, []byte{1}),
		nl.NewRtAttr(unix.ETHTOOL_A_LINKMODES_SPEED, nl.Uint32Attr(25000)),
		nl.NewRtAttr(unix.ETHTOOL_A_LINKMODES_DUPLEX, []byte{1}),
	))
	if err != nil || m != (LinkModes{SpeedMbps: 25000, Duplex: "full", Autoneg: true}) {
		t.Fatalf("link modes = %+v, %v", m, err)
	}
	// No carrier: SPEED_UNKNOWN and DUPLEX_UNKNOWN.
	m, err = parseLinkModes(reply(
		nl.NewRtAttr(unix.ETHTOOL_A_LINKMODES_SPEED, nl.Uint32Attr(0xffffffff)),
		nl.NewRtAttr(unix.ETHTOOL_A_LINKMODES_DUPLEX, []byte{0xff}),
	))
	if err != nil || m != (LinkModes{}) {
		t.Fatalf("unknown link modes = %+v, %v", m, err)
	}
}

func TestParseLinkState(t *testing.T) {
	s, err := parseLinkState(reply(
		nl.NewRtAttr(unix.ETHTOOL_A_LINKSTATE_LINK, []byte{0}),
		nl.NewRtAttr(unix.ETHTOOL_A_LINKSTATE_EXT_STATE, []byte{4}),
	))
	if err != nil || s != (LinkState{ExtState: "no-cable"}) {
		t.Fatalf("link state = %+v, %v", s, err)
	}
}

// The FEC counters are arrays: the total, then one value per lane.
func TestParseFEC(t *testing.T) {
	stats := nl.NewRtAttr(fecStats|unix.NLA_F_NESTED, nil)
	stats.AddRtAttr(fecStatCorrected, u64s(1200, 1000, 200))
	stats.AddRtAttr(fecStatUncorr, u64s(3, 3, 0))
	f, err := parseFEC(reply(nl.NewRtAttr(fecActive, nl.Uint32Attr(unix.ETHTOOL_LINK_MODE_FEC_RS_BIT)), stats))
	if err != nil || f != (FEC{Active: "rs", Corrected: 1200, Uncorrectable: 3, Stats: true}) {
		t.Fatalf("FEC = %+v, %v", f, err)
	}
	f, err = parseFEC(reply(nl.NewRtAttr(fecActive, nl.Uint32Attr(unix.ETHTOOL_LINK_MODE_FEC_NONE_BIT))))
	if err != nil || f != (FEC{Active: "off"}) {
		t.Fatalf("FEC off = %+v, %v", f, err)
	}
}

// On a veth pair the kernel reports 10000 Mb/s full duplex and the link
// state. It needs CAP_NET_ADMIN to create the pair.
func TestLinkKernel(t *testing.T) {
	veth := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "bootyeth0"}, PeerName: "bootyeth1"}
	if err := netlink.LinkAdd(veth); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EOPNOTSUPP) {
			t.Skipf("no veth: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(veth) })
	for _, name := range []string{"bootyeth0", "bootyeth1"} {
		l, _ := netlink.LinkByName(name)
		_ = netlink.LinkSetUp(l)
	}
	m, err := GetLinkModes("bootyeth0")
	if err != nil || m.SpeedMbps != 10000 || m.Duplex != "full" {
		t.Fatalf("veth link modes = %+v, %v", m, err)
	}
	if _, err := GetLinkState("bootyeth0"); err != nil {
		t.Fatalf("veth link state: %v", err)
	}
	if _, err := GetFEC("bootyeth0"); !IsNotSupported(err) {
		t.Fatalf("veth FEC = %v, want EOPNOTSUPP", err)
	}
}
