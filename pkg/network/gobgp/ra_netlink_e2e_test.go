//go:build linux && linux_e2e

package gobgp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/net/ipv6"

	"github.com/telekom/BOOTy/pkg/network"
)

// raTestVeth creates the veth pair name/peer without DAD in a new network
// namespace, both ends up.
func raTestVeth(t *testing.T, name, peer string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("test requires root (run with sudo)")
	}
	enterTestNetns(t)
	for _, k := range []string{"all", "default"} {
		_ = os.WriteFile("/proc/sys/net/ipv6/conf/"+k+"/accept_dad", []byte("0"), 0o644) //nolint:gosec // test sysctl
	}
	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: name}, PeerName: peer}); err != nil {
		t.Fatalf("add veth: %v", err)
	}
	for _, n := range []string{name, peer} {
		l, err := netlink.LinkByName(n)
		if err != nil {
			t.Fatal(err)
		}
		if err := netlink.LinkSetUp(l); err != nil {
			t.Fatal(err)
		}
	}
}

// joinedAllRouters reports whether nic is in ff02::2 (igmp6 of the
// thread's network namespace).
func joinedAllRouters(t *testing.T, nic string) bool {
	t.Helper()
	b, err := os.ReadFile("/proc/thread-self/net/igmp6")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 2 && f[1] == nic && f[2] == "ff020000000000000000000000000002" {
			return true
		}
	}
	return false
}

// sendICMPv6 sends msg from nic to dst with hop limit 255.
func sendICMPv6(t *testing.T, nic string, dst net.IP, msg []byte) {
	t.Helper()
	conn, err := network.ListenICMPv6(context.Background(), nic)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := ipv6.NewPacketConn(conn).SetMulticastHopLimit(255); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo(msg, &net.IPAddr{IP: dst, Zone: nic}); err != nil {
		t.Fatal(err)
	}
}

// listenRS joins ff02::2 on the NIC while it listens and reports a Router
// Solicitation that the switch sends to ff02::2 (RFC 4861 section 6.2.2).
func TestListenRSJoinsAllRouters(t *testing.T) {
	raTestVeth(t, "rs-b0", "rs-p0")
	rs := make(chan struct{}, 1)
	c, err := listenRS("rs-b0", rs)
	if err != nil {
		t.Fatal(err)
	}
	if !joinedAllRouters(t, "rs-b0") {
		_ = c.Close()
		t.Fatal("rs-b0 is not in ff02::2")
	}
	got := false
	for deadline := time.Now().Add(5 * time.Second); !got && time.Now().Before(deadline); {
		sendICMPv6(t, "rs-p0", net.IPv6linklocalallrouters, []byte{133, 0, 0, 0, 0, 0, 0, 0})
		select {
		case <-rs:
			got = true
		case <-time.After(200 * time.Millisecond): // link-local address not ready
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("no Router Solicitation reported")
	}
	if joinedAllRouters(t, "rs-b0") {
		t.Fatal("rs-b0 is still in ff02::2 after the close")
	}
}

// The leaf is found from its RA alone, with accept_ra 0 on the NIC, and
// the discovery sends no echo request.
func TestDiscoverLinkLocalPeerFromRA(t *testing.T) {
	raTestVeth(t, "dc-b0", "dc-p0")
	if err := os.WriteFile("/proc/sys/net/ipv6/conf/dc-b0/accept_ra", []byte("0"), 0o644); err != nil { //nolint:gosec // test sysctl
		t.Fatal(err)
	}
	conn, err := network.ListenICMPv6(context.Background(), "dc-p0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var echoes atomic.Int32
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if n > 0 && buf[0] == byte(ipv6.ICMPTypeEchoRequest) {
				echoes.Add(1)
			}
		}
	}()
	peer, err := net.InterfaceByName("dc-p0")
	if err != nil {
		t.Fatal(err)
	}
	// The socket is bound to dc-p0 in the test namespace; the goroutine
	// runs on another thread, so it sends without a zone.
	pc := ipv6.NewPacketConn(conn)
	if err := pc.SetMulticastHopLimit(255); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() {
		ra := routerAdvertisement(peer.HardwareAddr, raLifetime)
		for ctx.Err() == nil {
			time.Sleep(time.Second)
			_, _ = conn.WriteTo(ra, &net.IPAddr{IP: net.IPv6linklocalallnodes})
		}
	}()
	addr, err := discoverLinkLocalPeer(ctx, "dc-b0")
	if err != nil {
		t.Fatal(err)
	}
	addrs, _ := peer.Addrs()
	want := ""
	for _, a := range addrs {
		if ip := a.(*net.IPNet).IP; ip.IsLinkLocalUnicast() {
			want = ip.String() + "%dc-b0"
		}
	}
	if addr != want {
		t.Fatalf("peer %q, want %q", addr, want)
	}
	time.Sleep(200 * time.Millisecond)
	if n := echoes.Load(); n != 0 {
		t.Fatalf("%d echo requests during the discovery, want none", n)
	}
}

// Setup starts the RAs before it looks for the unnumbered peers, so the
// leaf learns the BOOTy address while BOOTy waits for the leaf.
func TestUnderlaySetupAdvertisesBeforeDiscovery(t *testing.T) {
	raTestVeth(t, "su-b0", "su-p0")
	u := NewUnderlayTier(&Config{
		ASN: 65000, RouterID: "192.0.2.17", PeerMode: network.PeerModeUnnumbered,
		Interfaces: []string{"su-b0"}, ListenPort: -1, MTU: 1500,
	})
	u.log = slog.Default()
	advertising := false
	u.discoverPeer = func(context.Context, string) (string, error) {
		advertising = u.stopRA != nil
		return "", errors.New("no leaf in this test")
	}
	ctx := context.Background()
	if err := u.Setup(ctx); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := u.Teardown(ctx); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !advertising {
		t.Fatal("no RAs while Setup looked for the peer")
	}
}

// sendRA sends nothing and reports false while the link-local address is
// tentative and without carrier, and true once the address is usable.
func TestSendRAWaitsForDADAndCarrier(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("test requires root (run with sudo)")
	}
	enterTestNetns(t)
	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "sr-b0"}, PeerName: "sr-p0"}); err != nil {
		t.Fatalf("add veth: %v", err)
	}
	for k, v := range map[string]string{"accept_dad": "1", "dad_transmits": "3"} {
		if err := os.WriteFile("/proc/sys/net/ipv6/conf/sr-b0/"+k, []byte(v), 0o644); err != nil { //nolint:gosec // test sysctl
			t.Fatal(err)
		}
	}
	setUp := func(name string, up bool) {
		t.Helper()
		l, err := netlink.LinkByName(name)
		if err != nil {
			t.Fatal(err)
		}
		set := netlink.LinkSetUp
		if !up {
			set = netlink.LinkSetDown
		}
		if err := set(l); err != nil {
			t.Fatal(err)
		}
	}
	setUp("sr-p0", true)
	setUp("sr-b0", true)
	if sendRA("sr-b0", raLifetime) {
		t.Fatal("RA sent with a tentative link-local address")
	}
	ok := false
	for deadline := time.Now().Add(10 * time.Second); !ok && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		ok = sendRA("sr-b0", raLifetime)
	}
	if !ok {
		t.Fatal("no RA after DAD")
	}
	setUp("sr-p0", false) // sr-b0 loses carrier
	time.Sleep(100 * time.Millisecond)
	if sendRA("sr-b0", raLifetime) {
		t.Fatal("RA sent without carrier")
	}
}

// A failed setupBGP stops the RAs that Setup started.
func TestUnderlaySetupFailureStopsRAs(t *testing.T) {
	raTestVeth(t, "sf-b0", "sf-p0")
	p, err := netlink.LinkByName("sf-p0")
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetDown(p); err != nil { // no carrier: no placeholder peer
		t.Fatal(err)
	}
	u := NewUnderlayTier(&Config{
		ASN: 65000, RouterID: "192.0.2.17", PeerMode: network.PeerModeUnnumbered,
		Interfaces: []string{"sf-b0"}, ListenPort: -1, MTU: 1500,
	})
	u.log = slog.Default()
	var done chan struct{}
	u.discoverPeer = func(context.Context, string) (string, error) {
		done = u.raDone
		return "", errors.New("no leaf in this test")
	}
	ctx := context.Background()
	if err := u.Setup(ctx); err == nil {
		_ = u.Teardown(ctx)
		t.Fatal("Setup without a peer succeeded")
	}
	defer func() { _ = u.Teardown(ctx) }()
	if done == nil {
		t.Fatal("no RAs while Setup looked for the peer")
	}
	select {
	case <-done:
	default:
		t.Fatal("RAs still running after the failed Setup")
	}
	if u.stopRA != nil {
		t.Fatal("stopRA still set after the failed Setup")
	}
}
