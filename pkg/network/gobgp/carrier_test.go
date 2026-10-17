//go:build linux

package gobgp

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/network"
)

func linkUpdate(name string, flags uint32) netlink.LinkUpdate {
	u := netlink.LinkUpdate{Link: &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name}}}
	u.IfInfomsg = nl.IfInfomsg{IfInfomsg: unix.IfInfomsg{Flags: flags}}
	return u
}

// Carrier loss stops the session once, a repeated loss does nothing, the
// carrier coming back sends an RA and starts the session once, and other
// interfaces are ignored.
// IFF_LOWER_UP is the carrier; IFF_UP alone is not.
func TestCarrierWatchDisableEnable(t *testing.T) {
	var calls []string
	w := &carrierWatch{
		peers: map[string]string{"ens1f0np0": "fe80::1%ens1f0np0"},
		down:  map[string]bool{},
		disable: func(_ context.Context, addr string) error {
			calls = append(calls, "disable "+addr)
			return nil
		},
		enable: func(_ context.Context, addr string) error {
			calls = append(calls, "enable "+addr)
			return nil
		},
		announce: func(iface string) { calls = append(calls, "RA "+iface) },
		log:      slog.Default(),
	}
	steps := []struct {
		update netlink.LinkUpdate
		want   []string
	}{
		{linkUpdate("ens1f0np0", unix.IFF_UP|unix.IFF_LOWER_UP), nil},
		{linkUpdate("ens1f0np0", unix.IFF_UP), []string{"disable fe80::1%ens1f0np0"}},
		{linkUpdate("ens1f0np0", unix.IFF_UP), []string{"disable fe80::1%ens1f0np0"}},
		{linkUpdate("eth9", 0), []string{"disable fe80::1%ens1f0np0"}},
		{linkUpdate("ens1f0np0", unix.IFF_UP|unix.IFF_LOWER_UP), []string{"disable fe80::1%ens1f0np0", "RA ens1f0np0", "enable fe80::1%ens1f0np0"}},
		{linkUpdate("ens1f0np0", unix.IFF_UP|unix.IFF_LOWER_UP), []string{"disable fe80::1%ens1f0np0", "RA ens1f0np0", "enable fe80::1%ens1f0np0"}},
		{netlink.LinkUpdate{}, []string{"disable fe80::1%ens1f0np0", "RA ens1f0np0", "enable fe80::1%ens1f0np0"}},
	}
	for i, s := range steps {
		w.handleUpdate(s.update)
		if len(calls) != len(s.want) {
			t.Fatalf("step %d: calls = %v, want %v", i, calls, s.want)
		}
		for j := range s.want {
			if calls[j] != s.want[j] {
				t.Fatalf("step %d: calls = %v, want %v", i, calls, s.want)
			}
		}
	}
}

// The fast-external-failover gate: off globally means no peers; a session
// with it off, a numbered session and an interface without a peer address
// are left out.
func TestCarrierPeers(t *testing.T) {
	sessions := []network.BGPSession{
		{Name: "ens1f0np0", Interface: true, FastExternalFailover: true},
		{Name: "ens1f1np1", Interface: true, FastExternalFailover: false},
		{Name: "192.0.2.1", FastExternalFailover: true},
		{Name: "ens1f2np2", Interface: true, FastExternalFailover: true},
	}
	iface := map[string]string{"ens1f0np0": "fe80::1%ens1f0np0", "ens1f1np1": "fe80::2%ens1f1np1", "192.0.2.1": "192.0.2.1"}
	for _, tt := range []struct {
		name   string
		global bool
		want   []string
	}{{"global off", false, nil}, {"global on", true, []string{"ens1f0np0"}}} {
		got := carrierPeers(&Config{FastExternalFailover: tt.global, Sessions: sessions}, iface)
		if len(got) != len(tt.want) {
			t.Errorf("%s: peers = %v, want %v", tt.name, got, tt.want)
		}
		for _, n := range tt.want {
			if got[n] != iface[n] {
				t.Errorf("%s: peers = %v, want %v", tt.name, got, tt.want)
			}
		}
	}
}

// The BGP_PEER_MODE peers: all unnumbered peers when the setting is on.
func TestCarrierPeersNative(t *testing.T) {
	iface := map[string]string{"ens1f0np0": "fe80::1%ens1f0np0"}
	if got := carrierPeers(&Config{FastExternalFailover: true}, iface); got["ens1f0np0"] == "" {
		t.Errorf("native peers on = %v, want ens1f0np0", got)
	}
	if got := carrierPeers(&Config{}, iface); len(got) != 0 {
		t.Errorf("native peers off = %v, want none", got)
	}
}

// ignore_routes_with_linkdown is 1 for IPv4 and IPv6 of every session NIC.
func TestIgnoreLinkdownRoutes(t *testing.T) {
	root := t.TempDir()
	for _, fam := range []string{"ipv4", "ipv6"} {
		if err := os.MkdirAll(filepath.Join(root, fam, "conf", "ens1f0np0"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if errs := ignoreLinkdownRoutes(root, []string{"ens1f0np0"}); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, fam := range []string{"ipv4", "ipv6"} {
		data, err := os.ReadFile(filepath.Join(root, fam, "conf", "ens1f0np0", "ignore_routes_with_linkdown"))
		if err != nil || string(data) != "1" {
			t.Errorf("%s: %q %v, want 1", fam, data, err)
		}
	}
	if errs := ignoreLinkdownRoutes(root, []string{"missing0"}); len(errs) != 2 {
		t.Errorf("missing NIC: %d errors, want 2", len(errs))
	}
}

// Leading-edge debounce: the first event reconciles at once, an event
// within the window waits for its rest, a scheduled reconcile absorbs more
// events.
func TestDebounce(t *testing.T) {
	for _, tt := range []struct {
		pending   bool
		sinceLast time.Duration
		now       bool
		wait      time.Duration
	}{
		{false, time.Hour, true, 0},
		{false, fibDebounce, true, 0},
		{false, 50 * time.Millisecond, false, fibDebounce - 50*time.Millisecond},
		{true, time.Hour, false, 0},
	} {
		now, wait := debounce(tt.pending, tt.sinceLast)
		if now != tt.now || wait != tt.wait {
			t.Errorf("debounce(%v, %s) = %v %s, want %v %s", tt.pending, tt.sinceLast, now, wait, tt.now, tt.wait)
		}
	}
}

// The RA is sent again after raRepeat; stopping the watch stops a pending
// repeat.
func TestCarrierWatchRARepeat(t *testing.T) {
	sent := make(chan string, 8)
	newWatch := func() *carrierWatch {
		return &carrierWatch{
			peers:    map[string]string{"ens1f0np0": "fe80::1%ens1f0np0"},
			down:     map[string]bool{"ens1f0np0": true},
			disable:  func(context.Context, string) error { return nil },
			enable:   func(context.Context, string) error { return nil },
			announce: func(iface string) { sent <- iface },
			raRepeat: 50 * time.Millisecond,
			log:      slog.Default(),
		}
	}
	count := func(within time.Duration) int {
		n, deadline := 0, time.After(within)
		for {
			select {
			case <-sent:
				n++
			case <-deadline:
				return n
			}
		}
	}
	w := newWatch()
	w.handle("ens1f0np0", true)
	if n := count(300 * time.Millisecond); n != 2 {
		t.Fatalf("RAs after carrier up = %d, want 2", n)
	}
	w = newWatch()
	w.handle("ens1f0np0", true)
	w.stopTimers()
	if n := count(300 * time.Millisecond); n != 1 {
		t.Fatalf("RAs after carrier up and stop = %d, want 1", n)
	}
}

// The start waits startDelay after the carrier came back; a carrier
// loss or the end of the watch in that time cancels it.
func TestCarrierWatchDelayedStart(t *testing.T) {
	started := make(chan string, 4)
	newWatch := func() *carrierWatch {
		return &carrierWatch{
			peers:      map[string]string{"ens1f0np0": "fe80::1%ens1f0np0"},
			down:       map[string]bool{"ens1f0np0": true},
			disable:    func(context.Context, string) error { return nil },
			enable:     func(_ context.Context, addr string) error { started <- addr; return nil },
			announce:   func(string) {},
			startDelay: 100 * time.Millisecond,
			log:        slog.Default(),
		}
	}
	starts := func(within time.Duration) int {
		n, deadline := 0, time.After(within)
		for {
			select {
			case <-started:
				n++
			case <-deadline:
				return n
			}
		}
	}
	w := newWatch()
	w.handle("ens1f0np0", true)
	if n := starts(50 * time.Millisecond); n != 0 {
		t.Fatalf("starts before the delay = %d, want 0", n)
	}
	if n := starts(150 * time.Millisecond); n != 1 {
		t.Fatalf("starts after the delay = %d, want 1", n)
	}
	w = newWatch()
	w.handle("ens1f0np0", true)
	w.handle("ens1f0np0", false)
	if n := starts(250 * time.Millisecond); n != 0 {
		t.Fatalf("starts after a new carrier loss = %d, want 0", n)
	}
	w = newWatch()
	w.handle("ens1f0np0", true)
	w.stopTimers()
	if n := starts(250 * time.Millisecond); n != 0 {
		t.Fatalf("starts after the watch stopped = %d, want 0", n)
	}
}

// The carrier coming back starts the session as a new GoBGP peer 1 s
// later. It connects 1.5 to 2 s after its start, not after the 5 s Idle
// hold of an enabled peer in GoBGP v4, and after the leaf, which
// connects about 2 s after the link. A plain listener stands in for the
// leaf, which on a real carrier loss never got the NOTIFICATION.
func TestCarrierWatchStartsNewPeer(t *testing.T) {
	ctx := context.Background()
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	accepted := make(chan time.Time, 64)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- time.Now()
			_ = c.Close()
		}
	}()
	booty := startServer(t, 4210000017, "192.0.2.17", -1)
	const addr = "127.0.0.1"
	peer := &apipb.Peer{
		Conf:      &apipb.PeerConf{NeighborAddress: addr, PeerAsn: 65500},
		Transport: &apipb.Transport{RemotePort: uint32(l.Addr().(*net.TCPAddr).Port)}, //nolint:gosec // test port
		Timers:    bgpTimers(&Config{ConnectRetry: 5, KeepaliveInterval: 3, HoldTime: 9}),
	}
	if err := booty.AddPeer(ctx, &apipb.AddPeerRequest{Peer: peer}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("no connection attempt of the new peer")
	}
	w := newCarrierWatch(booty, map[string]string{"eth1": addr}, map[string]*apipb.Peer{addr: peer}, slog.Default())
	w.announce = func(string) {}
	t.Cleanup(w.stopTimers)
	w.handle("eth1", false)
	time.Sleep(time.Second)
	start := time.Now()
	w.handle("eth1", true)
	for {
		select {
		case at := <-accepted:
			if at.Before(start) {
				continue
			}
			if d := at.Sub(start); d < 2400*time.Millisecond || d > 3500*time.Millisecond {
				t.Fatalf("first connection %s after the carrier, want 2.5 to 3 s", d)
			}
			return
		case <-time.After(5 * time.Second):
			t.Fatal("no connection within 5 s after the carrier came back")
		}
	}
}
