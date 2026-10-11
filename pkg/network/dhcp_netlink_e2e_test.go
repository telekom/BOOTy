//go:build linux && linux_e2e

package network

import (
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/digineo/go-dhclient"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// TestDHCPNetlinkLeaseAppliesTwice applies the same lease twice to a real
// dummy interface, like a network retry does. The second apply hits an
// existing address and must still report the lease as bound. A lease
// without a subnet mask must not panic.
func TestDHCPNetlinkLeaseAppliesTwice(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("test requires root (run with sudo)")
	}
	enterTestNetns(t)
	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dhcp-e2e0"}}
	if err := netlink.LinkAdd(link); err != nil {
		t.Fatalf("add dummy: %v", err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	d := NewDHCPMode()
	lease := &dhclient.Lease{FixedAddress: net.ParseIP("198.51.100.7")}
	for attempt := 1; attempt <= 2; attempt++ {
		leased := make(chan struct{}, 1)
		var winner dhcpWinner
		d.onBound(link, link.Name, leased, &winner)(lease)
		select {
		case <-leased:
		default:
			t.Fatalf("attempt %d: lease was not reported as bound", attempt)
		}
	}

	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		t.Fatalf("list addresses: %v", err)
	}
	if len(addrs) != 1 || addrs[0].IPNet.String() != "198.51.100.7/24" {
		t.Fatalf("addresses = %v, want 198.51.100.7/24", addrs)
	}
}

// enterTestNetns runs the rest of the test in a new network namespace, so
// the host interfaces and routes are never touched.
func enterTestNetns(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	orig, err := netns.Get()
	if err != nil {
		t.Fatalf("get netns: %v", err)
	}
	ns, err := netns.New()
	if err != nil {
		t.Fatalf("new netns: %v", err)
	}
	t.Cleanup(func() {
		_ = netns.Set(orig)
		_ = ns.Close()
		_ = orig.Close()
		runtime.UnlockOSThread()
	})
}
