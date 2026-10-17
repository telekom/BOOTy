//go:build linux

package sriov

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestVFStepsOrderAndSelection(t *testing.T) {
	all := vfSteps(&VFSettings{Trust: ptr(true), SpoofChk: ptr(false), VLAN: ptr(10), MaxTxRate: ptr(5), LinkState: "enable"}, "02:00:00:00:00:01")
	names := make([]string, 0, len(all))
	for _, s := range all {
		names = append(names, s.name)
	}
	if got := strings.Join(names, ","); got != "mac,vlan,rate,spoofchk,trust,state" {
		t.Fatalf("steps %s", got)
	}
	if len(vfSteps(&VFSettings{}, "")) != 0 {
		t.Fatal("no settings must make no calls")
	}
	if len(vfSteps(&VFSettings{QoS: ptr(1)}, "")) != 1 || len(vfSteps(&VFSettings{MinTxRate: ptr(1)}, "")) != 1 {
		t.Fatal("QoS alone and min rate alone must make one call each")
	}
	bad := vfSteps(&VFSettings{}, "zz")
	if err := bad[0].set(&netlink.Dummy{}, 0); err == nil {
		t.Fatal("a broken MAC must fail")
	}
}

func TestLinkStateValue(t *testing.T) {
	if linkStateValue("enable") != netlink.VF_LINK_STATE_ENABLE || linkStateValue("disable") != netlink.VF_LINK_STATE_DISABLE ||
		linkStateValue("auto") != netlink.VF_LINK_STATE_AUTO || deref(nil) != 0 || wrapNL(nil) != nil {
		t.Fatal("helpers")
	}
}

func TestForwardingRoute(t *testing.T) {
	cidr := func(s string) *net.IPNet {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	cases := []struct {
		r    netlink.Route
		want bool
	}{
		{netlink.Route{Type: unix.RTN_UNICAST}, true}, // default route
		{netlink.Route{Type: unix.RTN_UNICAST, Dst: cidr("10.0.0.0/8")}, true},
		{netlink.Route{Type: unix.RTN_UNICAST, Dst: cidr("2001:db8::/64")}, true},
		{netlink.Route{Type: unix.RTN_UNICAST, Dst: cidr("fe80::/64")}, false},
		{netlink.Route{Type: unix.RTN_MULTICAST, Dst: cidr("ff00::/8")}, false},
		{netlink.Route{Type: unix.RTN_LOCAL, Dst: cidr("fe80::1/128")}, false},
	}
	for _, c := range cases {
		if got := forwardingRoute(&c.r); got != c.want {
			t.Errorf("%+v = %v", c.r, got)
		}
	}
	r := &netlink.Route{MultiPath: []*netlink.NexthopInfo{{LinkIndex: 3}, {LinkIndex: 7}}}
	if !routeUses(r, 7) || routeUses(r, 4) || !routeUses(&netlink.Route{LinkIndex: 4}, 4) {
		t.Fatal("routeUses")
	}
}

func TestKernelRuntimeSetNumVFs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "p0", "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	k := &KernelRuntime{SysNet: dir}
	if err := k.SetNumVFs(&PF{Interface: "p0"}, 4); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "p0", "device", "sriov_numvfs")); string(b) != "4" {
		t.Fatalf("sriov_numvfs = %q", b)
	}
	if err := k.SetNumVFs(&PF{Interface: "missing"}, 1); err == nil {
		t.Fatal("missing device must fail")
	}
	if err := k.SetEswitch(&PF{}, "offload"); err == nil {
		t.Fatal("unknown mode must fail")
	}
	if NewKernelRuntime().SysNet != "/sys/class/net" {
		t.Fatal("default SysNet")
	}
}
