//go:build linux

package network

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/google/gopacket/layers"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestDHCPMode_Setup(t *testing.T) {
	// Fake an empty link list, so the test never probes the host NICs and
	// does not depend on root or on a DHCP server being reachable.
	t.Cleanup(snapshotNICFns())
	linkList = func() ([]netlink.Link, error) { return nil, nil }
	sleepFunc = func(time.Duration) {}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := &DHCPMode{}
	err := d.Setup(ctx, &Config{})
	if err == nil {
		t.Fatal("Setup() = nil, want error when no interface gets a DHCP lease")
	}
	if len(d.clients) != 0 {
		t.Fatalf("Setup() kept DHCP clients after error %v", err)
	}
}

func TestDHCPMode_SetupCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &DHCPMode{}
	err := d.Setup(ctx, &Config{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Setup() error = %v, want context.Canceled", err)
	}
	if len(d.clients) != 0 {
		t.Fatal("Setup() kept DHCP clients after cancellation")
	}
}

func TestDHCPMode_Teardown(t *testing.T) {
	d := &DHCPMode{}
	if err := d.Teardown(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForHTTP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := WaitForHTTP(context.Background(), srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForHTTP_Timeout(t *testing.T) {
	// Use localhost with a port nothing is listening on — connection refused is instant.
	err := WaitForHTTP(context.Background(), "http://127.0.0.1:19", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWaitForHTTP_TimeoutBoundsInFlightRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	start := time.Now()
	err := WaitForHTTP(context.Background(), srv.URL, 150*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > time.Second {
		t.Fatalf("WaitForHTTP took %s, want request bounded by timeout", elapsed)
	}
}

func TestWaitForHTTP_EmptyTarget(t *testing.T) {
	err := WaitForHTTP(context.Background(), "", 1*time.Second)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
}

func TestWaitForHTTP_NonPositiveTimeout(t *testing.T) {
	err := WaitForHTTP(context.Background(), "http://127.0.0.1:19", 0)
	if err == nil {
		t.Fatal("expected error for non-positive timeout")
	}
	if !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("error = %q, want positive-timeout validation", err.Error())
	}
}

func TestWaitForHTTP_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	err := WaitForHTTP(ctx, "http://192.0.2.1:1", 5*time.Second)
	if err == nil {
		t.Fatal("expected context cancel error")
	}
}

func TestWaitForHTTP_AuthUnauthorized(t *testing.T) {
	// A 401 response proves network connectivity — the server is reachable
	// even though the request is not authenticated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := WaitForHTTP(context.Background(), srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("expected 401 to count as connectivity, got error: %v", err)
	}
}

func TestWaitForHTTP_RedactsTargetInLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	target := strings.Replace(srv.URL, "http://", "http://user:secret@", 1) + "/image.raw?token=abc#frag"
	err := WaitForHTTP(context.Background(), target, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := logs.String()
	for _, leaked := range []string{"user", "secret", "token=abc", "frag"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("logs leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "/image.raw") {
		t.Fatalf("logs = %q, want redacted path context", got)
	}
}

func TestDHCPMode_WaitForConnectivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &DHCPMode{}
	err := d.WaitForConnectivity(context.Background(), srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDHCPSetup_ContextCancelPropagates verifies that canceling the context
// terminates all concurrent NIC probes promptly.
// In environments with no physical NICs, Setup returns immediately with an
// error — that case is also valid and proves no blocking.
func TestDHCPSetup_ContextCancelPropagates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	d := &DHCPMode{}
	_ = d.Setup(ctx, &Config{})
	elapsed := time.Since(start)

	if elapsed > 700*time.Millisecond {
		t.Fatalf("Setup took %v — context cancellation did not propagate promptly", elapsed)
	}
}

func TestNewDHCPClientRequestsDefaultParams(t *testing.T) {
	for _, hostname := range []string{"(none)", ""} {
		client := newDHCPClient(&net.Interface{Name: "eth0"}, hostname, nil)

		var params []byte
		var gotHostname []byte
		for _, opt := range client.DHCPOptions {
			switch opt.Type {
			case layers.DHCPOptParamsRequest:
				params = opt.Data
			case layers.DHCPOptHostname:
				gotHostname = opt.Data
			}
		}
		for _, want := range []layers.DHCPOpt{layers.DHCPOptSubnetMask, layers.DHCPOptRouter, layers.DHCPOptDNS} {
			if !bytes.Contains(params, []byte{byte(want)}) {
				t.Errorf("hostname %q: parameter request list %v misses %s", hostname, params, want)
			}
		}
		if string(gotHostname) != hostname {
			t.Errorf("hostname option = %q, want %q", gotHostname, hostname)
		}
	}
}

func TestLeaseAddrDefaultsMissingNetmask(t *testing.T) {
	addr, err := leaseAddr(&dhclient.Lease{FixedAddress: net.ParseIP("10.0.2.15")})
	if err != nil {
		t.Fatalf("leaseAddr() error = %v", err)
	}
	if got := addr.IPNet.String(); got != "10.0.2.15/8" {
		t.Fatalf("leaseAddr() = %s, want 10.0.2.15/8", got)
	}
	if _, err := leaseAddr(&dhclient.Lease{}); err == nil {
		t.Fatal("leaseAddr() without address should fail")
	}
	if _, err := leaseAddr(&dhclient.Lease{FixedAddress: net.IPv4zero}); err == nil {
		t.Fatal("leaseAddr() with 0.0.0.0 should fail")
	}
	if _, err := leaseAddr(&dhclient.Lease{FixedAddress: net.ParseIP("10.0.2.15"), Netmask: net.IPMask{255, 0, 255, 0}}); err == nil {
		t.Fatal("leaseAddr() with a non-contiguous mask should fail")
	}
}

// stubDHCPNetlink replaces the netlink calls of onBound for one test.
func stubDHCPNetlink(t *testing.T) {
	t.Helper()
	origAddr, origAddrDel, origRoute, origRouteDel := dhcpAddrReplace, dhcpAddrDel, dhcpRouteAdd, dhcpRouteDel
	t.Cleanup(func() {
		dhcpAddrReplace, dhcpAddrDel, dhcpRouteAdd, dhcpRouteDel = origAddr, origAddrDel, origRoute, origRouteDel
	})
	dhcpAddrReplace = func(netlink.Link, *netlink.Addr) error { return nil }
	dhcpAddrDel = func(netlink.Link, *netlink.Addr) error { return nil }
	dhcpRouteAdd = func(*netlink.Route) error { return nil }
	dhcpRouteDel = func(*netlink.Route) error { return nil }
}

func dummyLink(name string, index int) *netlink.Dummy {
	return &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name, Index: index}}
}

func lease24(ip string, router ...string) *dhclient.Lease {
	l := &dhclient.Lease{FixedAddress: net.ParseIP(ip), Netmask: net.CIDRMask(24, 32)}
	for _, r := range router {
		l.Router = append(l.Router, net.ParseIP(r))
	}
	return l
}

func TestOnBoundReplacesStateAndTeardownRemovesIt(t *testing.T) {
	stubDHCPNetlink(t)
	var addrs, delAddrs []*netlink.Addr
	var routes, delRoutes []*netlink.Route
	dhcpAddrReplace = func(_ netlink.Link, a *netlink.Addr) error { addrs = append(addrs, a); return nil }
	dhcpAddrDel = func(_ netlink.Link, a *netlink.Addr) error { delAddrs = append(delAddrs, a); return nil }
	dhcpRouteAdd = func(r *netlink.Route) error { routes = append(routes, r); return nil }
	dhcpRouteDel = func(r *netlink.Route) error { delRoutes = append(delRoutes, r); return nil }

	d := NewDHCPMode()
	leased := make(chan string, 1)
	lease := lease24("10.0.2.15", "10.0.2.2")
	lease.OtherOptions = []dhclient.Option{{Type: layers.DHCPOptNTPServers, Data: []byte{10, 0, 2, 3}}}
	d.onBound(dummyLink("eth0", 7), "eth0", leased)(lease)
	t.Cleanup(func() { setDHCPNTPServers(nil) })
	if got := DHCPNTPServers(); len(got) != 1 || got[0] != "10.0.2.3" {
		t.Fatalf("DHCP NTP servers = %v, want [10.0.2.3]", got)
	}

	if len(leased) != 1 || <-leased != "eth0" {
		t.Fatal("lease was not reported as bound")
	}
	if len(addrs) != 1 || addrs[0].IPNet.String() != "10.0.2.15/24" {
		t.Fatalf("addresses = %v, want 10.0.2.15/24", addrs)
	}
	r := routes[0]
	if len(routes) != 1 || r.LinkIndex != 7 || !r.Gw.Equal(net.ParseIP("10.0.2.2")) || !r.Src.Equal(net.ParseIP("10.0.2.15")) ||
		r.Priority != dhcpRouteMetric || r.Protocol != unix.RTPROT_DHCP {
		t.Fatalf("default route = %+v, want via 10.0.2.2 src 10.0.2.15 on link 7, metric 100, proto dhcp", routes)
	}

	if err := d.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown() error = %v", err)
	}
	if len(delAddrs) != 1 || len(delRoutes) != 1 {
		t.Fatalf("Teardown removed addrs=%v routes=%v, want the lease address and route", delAddrs, delRoutes)
	}
}

func TestOnBoundIgnoresInvalidLease(t *testing.T) {
	stubDHCPNetlink(t)
	d := NewDHCPMode()
	leased := make(chan string, 1)
	d.onBound(dummyLink("eth0", 1), "eth0", leased)(&dhclient.Lease{})
	if len(leased) != 0 || len(d.nics) != 0 {
		t.Fatalf("an invalid lease was applied: leased=%d state=%v", len(leased), d.nics)
	}
}

// TestOnBoundAppliesEveryNIC: like networkd with "dhcp4: true" on every
// NIC, each NIC keeps its own lease and default route; a NIC whose lease
// fails does not stop the others.
func TestOnBoundAppliesEveryNIC(t *testing.T) {
	stubDHCPNetlink(t)
	var routes []string
	dhcpAddrReplace = func(link netlink.Link, _ *netlink.Addr) error {
		if link.Attrs().Name == "eth2" {
			return syscall.EPERM
		}
		return nil
	}
	dhcpRouteAdd = func(r *netlink.Route) error { routes = append(routes, r.Gw.String()); return nil }

	d := NewDHCPMode()
	leased := make(chan string, 3)
	d.onBound(dummyLink("eth0", 1), "eth0", leased)(lease24("10.0.2.15", "10.0.2.2"))
	d.onBound(dummyLink("eth1", 2), "eth1", leased)(lease24("10.0.3.15", "10.0.3.2"))
	d.onBound(dummyLink("eth2", 3), "eth2", leased)(lease24("10.0.4.15", "10.0.4.2"))

	if want := []string{"10.0.2.2", "10.0.3.2"}; !slices.Equal(routes, want) {
		t.Fatalf("default routes added = %v, want %v", routes, want)
	}
	if len(d.nics) != 2 || d.nics[1].addr.IPNet.String() != "10.0.2.15/24" || d.nics[2].addr.IPNet.String() != "10.0.3.15/24" {
		t.Fatalf("applied state = %v, want eth0 and eth1", d.nics)
	}
	if len(leased) != 2 {
		t.Fatalf("%d leases reported, want 2", len(leased))
	}
}

func TestOnBoundRouteFailureRollsBackAddress(t *testing.T) {
	stubDHCPNetlink(t)
	var rolledBack []string
	dhcpAddrDel = func(link netlink.Link, _ *netlink.Addr) error {
		rolledBack = append(rolledBack, link.Attrs().Name)
		return nil
	}
	dhcpRouteAdd = func(r *netlink.Route) error {
		if r.LinkIndex == 1 {
			return syscall.ENETUNREACH
		}
		return nil
	}

	d := NewDHCPMode()
	leased := make(chan string, 2)
	d.onBound(dummyLink("eth0", 1), "eth0", leased)(lease24("10.0.2.15", "10.0.2.2"))
	d.onBound(dummyLink("eth1", 2), "eth1", leased)(lease24("10.0.3.15", "10.0.3.2"))

	if len(leased) != 1 || <-leased != "eth1" {
		t.Fatal("want only the lease on eth1 reported")
	}
	if len(rolledBack) != 1 || rolledBack[0] != "eth0" {
		t.Fatalf("rolled back addresses on %v, want eth0", rolledBack)
	}
	if _, ok := d.nics[1]; ok {
		t.Fatalf("eth0 state = %v, want none", d.nics[1])
	}
}

func TestOnBoundAppliesRenewal(t *testing.T) {
	stubDHCPNetlink(t)
	var set, deleted []string
	var routeDels int
	dhcpAddrReplace = func(link netlink.Link, a *netlink.Addr) error {
		set = append(set, link.Attrs().Name+" "+a.IPNet.String())
		return nil
	}
	dhcpAddrDel = func(link netlink.Link, a *netlink.Addr) error {
		deleted = append(deleted, link.Attrs().Name+" "+a.IPNet.String())
		return nil
	}
	dhcpRouteAdd = func(*netlink.Route) error { return syscall.EEXIST } // a renewal adds the same route again
	dhcpRouteDel = func(*netlink.Route) error { routeDels++; return nil }

	d := NewDHCPMode()
	bound0 := d.onBound(dummyLink("eth0", 1), "eth0", make(chan string, 4))
	bound1 := d.onBound(dummyLink("eth1", 2), "eth1", make(chan string, 4))
	bound0(lease24("10.0.2.15", "10.0.2.2"))
	bound1(lease24("10.0.3.15", "10.0.3.2"))
	bound0(lease24("10.0.2.15", "10.0.2.2"))
	if routeDels != 0 {
		t.Fatalf("a renewal with the same router removed %d routes", routeDels)
	}
	bound0(lease24("10.0.2.16", "10.0.2.2"))

	if want := []string{"eth0 10.0.2.15/24", "eth1 10.0.3.15/24", "eth0 10.0.2.15/24", "eth0 10.0.2.16/24"}; !slices.Equal(set, want) {
		t.Fatalf("addresses set = %v, want %v", set, want)
	}
	if want := []string{"eth0 10.0.2.15/24"}; !slices.Equal(deleted, want) {
		t.Fatalf("addresses deleted = %v, want %v", deleted, want)
	}
	if routeDels != 1 {
		t.Fatalf("route deletes = %d, want the route with the old source", routeDels)
	}
	if len(d.nics) != 2 || d.nics[1].addr.IPNet.String() != "10.0.2.16/24" || d.nics[2].addr.IPNet.String() != "10.0.3.15/24" {
		t.Fatalf("applied state = %v, want eth0 10.0.2.16/24 and eth1 10.0.3.15/24", d.nics)
	}
}

func TestOnBoundRenewalRouteFailureKeepsPreviousLease(t *testing.T) {
	stubDHCPNetlink(t)
	var deleted []string
	dhcpAddrDel = func(_ netlink.Link, a *netlink.Addr) error {
		deleted = append(deleted, a.IPNet.String())
		return nil
	}
	failRoute := false
	dhcpRouteAdd = func(*netlink.Route) error {
		if failRoute {
			return syscall.ENETUNREACH
		}
		return nil
	}

	d := NewDHCPMode()
	bound := d.onBound(dummyLink("eth0", 1), "eth0", make(chan string, 2))
	bound(lease24("10.0.2.15", "10.0.2.2"))
	failRoute = true
	bound(lease24("10.0.2.16", "10.0.2.2"))

	if want := []string{"10.0.2.16/24"}; !slices.Equal(deleted, want) {
		t.Fatalf("deleted = %v, want only the new address rolled back", deleted)
	}
	if d.nics[1].addr.IPNet.String() != "10.0.2.15/24" || d.nics[1].route == nil {
		t.Fatalf("applied state = %v, want the previous lease 10.0.2.15/24 with its route", d.nics)
	}
}

func TestOnBoundRenewalDropsRouteAndRestoresMask(t *testing.T) {
	stubDHCPNetlink(t)
	var routeDels int
	dhcpRouteDel = func(*netlink.Route) error { routeDels++; return nil }
	var set []string
	dhcpAddrReplace = func(_ netlink.Link, a *netlink.Addr) error {
		set = append(set, a.IPNet.String())
		return nil
	}
	failRoute := false
	dhcpRouteAdd = func(*netlink.Route) error {
		if failRoute {
			return syscall.ENETUNREACH
		}
		return nil
	}

	d := NewDHCPMode()
	bound := d.onBound(dummyLink("eth0", 1), "eth0", make(chan string, 3))
	bound(lease24("10.0.2.15", "10.0.2.2"))
	// Renewal without a router option removes the old default route.
	bound(lease24("10.0.2.15"))
	if routeDels != 1 || d.nics[1].route != nil {
		t.Fatalf("route deletes = %d, applied state = %v, want the old default route removed", routeDels, d.nics)
	}

	// Renewal with a new mask whose route fails restores the old mask.
	failRoute = true
	bound(&dhclient.Lease{FixedAddress: net.ParseIP("10.0.2.15"), Netmask: net.CIDRMask(16, 32), Router: []net.IP{net.ParseIP("10.0.2.2")}})
	if want := []string{"10.0.2.15/24", "10.0.2.15/24", "10.0.2.15/16", "10.0.2.15/24"}; !slices.Equal(set, want) {
		t.Fatalf("addresses set = %v, want %v", set, want)
	}
}

func TestOnBoundRenewalTracksAddressWhenRollbackFails(t *testing.T) {
	stubDHCPNetlink(t)
	dhcpAddrDel = func(netlink.Link, *netlink.Addr) error { return syscall.EBUSY }
	failRoute := false
	dhcpRouteAdd = func(*netlink.Route) error {
		if failRoute {
			return syscall.ENETUNREACH
		}
		return nil
	}

	d := NewDHCPMode()
	bound := d.onBound(dummyLink("eth0", 1), "eth0", make(chan string, 2))
	bound(lease24("10.0.2.15", "10.0.2.2"))
	failRoute = true
	bound(lease24("10.0.2.16", "10.0.2.2"))

	if len(d.stale) != 1 || d.stale[0].addr.IPNet.String() != "10.0.2.16/24" {
		t.Fatalf("stale state = %v, want the address whose rollback failed tracked for Teardown", d.stale)
	}
}
