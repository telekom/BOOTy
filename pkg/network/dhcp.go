//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/google/gopacket/layers"
	"github.com/telekom/BOOTy/pkg/network/lldp"
	"github.com/vishvananda/netlink"
)

// DHCPMode implements the Mode interface with DHCPv4 on every physical
// interface, like systemd-networkd with the legacy ramdisk's netplan
// ("dhcp4: true" on every en* and eth* NIC): a client runs on every NIC for
// the whole run, every lease configures its NIC (address, a default route
// with the netplan metric, the MTU), and renewals update it. The DNS servers
// of all leases go to resolv.conf after the configured ones, like
// systemd-resolved with UseDNS=yes.
type DHCPMode struct {
	log *slog.Logger

	mu      sync.Mutex
	clients map[int]*nicClient  // by link index
	nics    map[int]dhcpApplied // by link index: what the current lease configured
	stale   []dhcpApplied       // state that could not be removed; Teardown retries
	// stopped is set before the clients stop; a lease that arrives after it
	// is not applied.
	stopped bool
	// expiry ends the lease of a NIC at its expiry time. go-dhclient v1.0.2
	// does not: after a failed renewal it renews again forever and never
	// reaches the expiry, so OnExpire only comes for a DHCPNAK.
	expiry map[int]leaseTimer
	mtus   map[int]dhcpMTU // by link index: MTU before the first lease changed it

	manageDNS bool     // false: the provisioner resolv.conf stays
	resolvers []string // configured DNS servers, written before the lease ones
	written   string   // the resolv.conf content last written
}

// nicClient is the running DHCP client of one NIC and how to start a fresh
// one for it.
type nicClient struct {
	client dhcpClient
	start  func() dhcpClient
}

// dhcpStart starts a client; tests replace it.
var dhcpStart = func(c dhcpClient) dhcpClient {
	c.Start()
	return c
}

// leaseTimer is the expiry timer of the lease of one NIC.
type leaseTimer struct {
	timer *time.Timer
	at    time.Time // the expiry time it was armed for
}

// dhcpAfterFunc is time.AfterFunc; tests replace it.
var dhcpAfterFunc = time.AfterFunc

// dhcpApplied is what a lease configured: address, routes, DNS and NTP
// servers.
type dhcpApplied struct {
	link   netlink.Link
	addr   *netlink.Addr
	routes []*netlink.Route
	dns    []string
	ntp    []string
	// domains are the lease domain (option 15) and search list (option
	// 119), search domains like networkd with netplan (the legacy
	// resolv.conf has them as "search").
	domains []string
}

// dhcpClient is the part of a go-dhclient client that DHCPMode uses.
type dhcpClient interface {
	Start()
	Stop()
}

// Seams for Setup tests.
var (
	dhcpInterfaces  = physicalInterfaces
	dhcpLinkByName  = netlink.LinkByName
	dhcpLinkSetUp   = netlink.LinkSetUp
	dhcpPrepareLink = promoteSecondaries
	dhcpNewClient   = func(iface *net.Interface, hostname string, onBound, onExpire dhclient.Callback) dhcpClient {
		return newDHCPClient(iface, hostname, dhcpClientID(iface.Name, lldp.MachineID()), onBound, onExpire)
	}
)

// promoteSecondaries sets net.ipv4.conf.<nic>.promote_secondaries=1, like
// systemd's sysctl defaults (50-default.conf) in the legacy ramdisk. A
// renewal with a new address in the same subnet adds it as a secondary
// address; without promotion, removing the old primary would remove the
// new address too.
func promoteSecondaries(ifname string) error {
	if err := os.WriteFile("/proc/sys/net/ipv4/conf/"+ifname+"/promote_secondaries", []byte("1"), 0o644); err != nil { //nolint:gosec // a sysctl
		return fmt.Errorf("set promote_secondaries on %s: %w", ifname, err)
	}
	return nil
}

// dhcpMTU is the MTU a NIC had before the first lease changed it, and the
// MTU the leases set.
type dhcpMTU struct {
	link      netlink.Link
	orig, set int
}

// dhcpMinMTU is the smallest lease MTU that is used. systemd-networkd
// ignores option 26 below the minimum DHCP packet size (RFC 2131: 576).
const dhcpMinMTU = 576

// dhcpRouteMetric is the metric of DHCP default routes: netplan writes
// RouteMetric=100 for ethernets, so the routes of all NICs are equal.
const dhcpRouteMetric = 100

// dhcpSetupTimeout bounds the wait for the first lease in Setup.
var dhcpSetupTimeout = 15 * time.Second

// NewDHCPMode creates a DHCPMode with a component logger.
func NewDHCPMode() *DHCPMode {
	return &DHCPMode{
		log: slog.Default().With("component", "dhcp"),
	}
}

// Setup starts a DHCP client on every physical interface and returns when
// the first lease is applied. The clients keep running: later leases, also
// on other NICs, configure their NIC too. Without a lease within 15 s every
// client is stopped and Setup fails.
func (d *DHCPMode) Setup(ctx context.Context, cfg *Config) error {
	if d.log == nil {
		d.log = slog.Default().With("component", "dhcp")
	}
	d.mu.Lock()
	d.manageDNS = cfg != nil && !cfg.KeepResolvConf
	d.resolvers = nil
	if d.manageDNS {
		for _, r := range strings.Split(cfg.DNSResolvers, ",") {
			if r = strings.TrimSpace(r); r != "" {
				d.resolvers = append(d.resolvers, r)
			}
		}
	}
	d.writeResolvers()
	d.mu.Unlock()
	ifaces, err := dhcpInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("listing interfaces: %w", err)
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no physical interfaces found for DHCP")
	}

	d.log.Info("Starting DHCP on all interfaces", "count", len(ifaces))
	d.mu.Lock()
	d.stopped = false
	d.mu.Unlock()
	leased := make(chan string, len(ifaces))
	hostname, _ := os.Hostname()
	for i := range ifaces {
		d.startClient(&ifaces[i], hostname, leased)
	}

	timer := time.NewTimer(dhcpSetupTimeout)
	defer timer.Stop()
	select {
	case name := <-leased:
		d.log.Info("DHCP succeeded", "interface", name)
		return nil
	case <-timer.C:
		// A lease applied while the timer fired counts: stopping now would
		// leave its address without a client.
		if d.stopUnlessLeased() {
			return fmt.Errorf("DHCP failed on all %d interfaces", len(ifaces))
		}
		d.log.Info("DHCP succeeded at the timeout")
		return nil
	case <-ctx.Done():
		d.stopClients()
		return fmt.Errorf("DHCP probing canceled: %w", ctx.Err())
	}
}

// startClient brings a NIC up and starts its DHCP client.
func (d *DHCPMode) startClient(iface *net.Interface, hostname string, leased chan<- string) {
	link, err := dhcpLinkByName(iface.Name)
	if err != nil {
		d.log.Warn("Cannot find link for DHCP", "interface", iface.Name, "error", err)
		return
	}
	if err := dhcpPrepareLink(iface.Name); err != nil {
		d.log.Warn("Cannot set promote_secondaries for DHCP", "interface", iface.Name, "error", err)
	}
	if err := dhcpLinkSetUp(link); err != nil {
		d.log.Warn("Cannot bring up link for DHCP", "interface", iface.Name, "error", err)
		return
	}
	start := func() dhcpClient { return dhcpStart(d.clientFor(iface, link, hostname, leased)) }
	nc := &nicClient{client: start(), start: start}
	d.mu.Lock()
	if d.clients == nil {
		d.clients = map[int]*nicClient{}
	}
	d.clients[link.Attrs().Index] = nc
	d.mu.Unlock()
}

// stopUnlessLeased stops the clients unless a lease is applied, and tells
// whether it stopped them. The check and the stop flag are atomic, so no
// lease is applied after it.
func (d *DHCPMode) stopUnlessLeased() bool {
	d.mu.Lock()
	if len(d.nics) > 0 {
		d.mu.Unlock()
		return false
	}
	d.mu.Unlock()
	d.stopClients()
	return true
}

// stopClients stops every DHCP client, all at once: go-dhclient's Stop
// waits up to its 5 s response timeout on a NIC without a server. A client
// is stopped once, by whoever takes it out of d.clients (go-dhclient panics
// on a second Stop).
func (d *DHCPMode) stopClients() {
	d.mu.Lock()
	d.stopped = true
	clients := d.clients
	d.clients = nil
	d.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Go(c.client.Stop)
	}
	wg.Wait()
}

// restartClient replaces the client of a NIC whose lease ended with a fresh
// one, which starts in INIT with a DHCPDISCOVER (RFC 2131 section 4.4.5),
// like networkd's client_initialize and client_start. go-dhclient keeps the
// old lease after failed renewals and would only send DHCPREQUESTs for it
// to the old server. The caller took nc out of d.clients and does not hold
// d.mu: Stop waits for the client, whose OnBound may wait for d.mu.
func (d *DHCPMode) restartClient(idx int, ifName string, nc *nicClient) {
	nc.client.Stop()
	fresh := nc.start()
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		fresh.Stop()
		return
	}
	if d.clients == nil {
		d.clients = map[int]*nicClient{}
	}
	d.clients[idx] = &nicClient{client: fresh, start: nc.start}
	d.mu.Unlock()
	d.log.Info("DHCP client restarted after the lease ended", "iface", ifName)
}

// clientFor builds the DHCP client of one NIC with its lease callbacks.
func (d *DHCPMode) clientFor(iface *net.Interface, link netlink.Link, hostname string, leased chan<- string) dhcpClient {
	return dhcpNewClient(iface, hostname, d.onBound(link, iface.Name, leased), d.onExpire(link, iface.Name))
}

// newDHCPClient builds a client that sends the options of networkd, in
// its order after the message type: client identifier (61), parameter
// request list (55), maximum message size (57, MTU less 28) and host name
// (12) when one is set. go-dhclient adds the requested address (50) and
// the server identifier (54) to a REQUEST after them.
func newDHCPClient(iface *net.Interface, hostname string, clientID []byte, onBound, onExpire dhclient.Callback) *dhclient.Client {
	client := &dhclient.Client{Iface: iface, OnBound: onBound, OnExpire: onExpire}
	client.AddOption(layers.DHCPOptClientID, clientID)
	for _, param := range networkdParams {
		client.AddParamRequest(param)
	}
	client.AddOption(layers.DHCPOptMaxMessageSize, dhcpMaxMessageSize(iface.MTU))
	if hostname != "" {
		client.AddOption(layers.DHCPOptHostname, []byte(hostname))
	}
	return client
}

// Netlink and resolv.conf seams for onBound tests.
var (
	dhcpAddrReplace    = replaceLeaseAddr
	dhcpAddrDel        = netlink.AddrDel
	dhcpRouteAdd       = netlink.RouteAppend
	dhcpRouteDel       = netlink.RouteDel
	dhcpSetMTU         = netlink.LinkSetMTU
	dhcpWriteResolvers = ConfigureResolvConf
)

// replaceLeaseAddr sets addr on link. The kernel keys IPv4 addresses by
// address and prefix, so AddrReplace alone would keep the same IP with an
// older prefix next to the new one. Those entries are removed after the new
// address is in place, so a failure never leaves the link without the lease.
func replaceLeaseAddr(link netlink.Link, addr *netlink.Addr) error {
	if err := netlink.AddrReplace(link, addr); err != nil {
		return err //nolint:wrapcheck // callers add context
	}
	existing, err := RetryDump(func() ([]netlink.Addr, error) { return netlink.AddrList(link, netlink.FAMILY_V4) })
	if err != nil {
		slog.Warn("failed to list addresses for DHCP prefix cleanup", "iface", link.Attrs().Name, "error", err)
		return nil
	}
	for i := range existing {
		a := &existing[i]
		if a.IP.Equal(addr.IP) && a.IPNet.String() != addr.IPNet.String() {
			if err := netlink.AddrDel(link, a); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
				slog.Warn("failed to remove DHCP address with an old prefix", "iface", link.Attrs().Name, "addr", a.IPNet, "error", err)
			}
		}
	}
	return nil
}

// onBound returns the callback of the client on one NIC. Every lease, the
// first and each renewal or rebind, configures that NIC. A lease that cannot
// be applied leaves the previous state of the NIC in place.
func (d *DHCPMode) onBound(link netlink.Link, ifName string, leased chan<- string) func(*dhclient.Lease) {
	return func(lease *dhclient.Lease) {
		addr, err := leaseAddr(lease)
		if err != nil {
			d.log.Warn("ignoring DHCP lease", "iface", ifName, "error", err)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.stopped {
			return
		}
		if err := d.apply(link, addr, lease); err != nil {
			d.log.Warn("failed to apply DHCP lease", "iface", ifName, "error", err)
			return
		}
		d.armExpiry(link, ifName, lease.Expire)
		d.publishNTP()
		d.log.Info("DHCP lease applied", "iface", ifName, "addr", addr.IPNet.String())
		select {
		case leased <- ifName:
		default:
		}
	}
}

// onExpire returns the callback for the end of a lease of one NIC that
// go-dhclient reports: a DHCPNAK, or an expiry it notices. RFC 2131
// sections 4.4.5 and 3.2 say the client then stops using the address and
// starts over in INIT, so the address and default route of the lease go.
// The client keeps running; its next lease is applied like any other.
func (d *DHCPMode) onExpire(link netlink.Link, ifName string) func(*dhclient.Lease) {
	return func(*dhclient.Lease) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.endLease(link, ifName)
	}
}

// armExpiry (re)starts the expiry timer of the lease of a NIC; each bound
// lease, also a renewal, moves it. A zero expiry disarms it. The caller
// holds d.mu.
func (d *DHCPMode) armExpiry(link netlink.Link, ifName string, at time.Time) {
	idx := link.Attrs().Index
	if old, ok := d.expiry[idx]; ok {
		old.timer.Stop()
		delete(d.expiry, idx)
	}
	if at.IsZero() {
		return
	}
	if d.expiry == nil {
		d.expiry = map[int]leaseTimer{}
	}
	d.expiry[idx] = leaseTimer{at: at, timer: dhcpAfterFunc(time.Until(at), func() {
		d.mu.Lock()
		// A renewal may have moved the expiry while this timer fired.
		if cur, ok := d.expiry[idx]; !ok || !cur.at.Equal(at) {
			d.mu.Unlock()
			return
		}
		d.log.Warn("DHCP lease expired without renewal", "iface", ifName)
		d.endLease(link, ifName)
		nc := d.clients[idx]
		delete(d.clients, idx)
		d.mu.Unlock()
		if nc != nil {
			d.restartClient(idx, ifName, nc)
		}
	})}
}

// endLease removes the address and default route of the lease of a NIC
// and its expiry timer. State that cannot be removed stays tracked for
// Teardown. The caller holds d.mu.
func (d *DHCPMode) endLease(link netlink.Link, ifName string) {
	idx := link.Attrs().Index
	if t, ok := d.expiry[idx]; ok {
		t.timer.Stop()
		delete(d.expiry, idx)
	}
	a, ok := d.nics[idx]
	if !ok {
		return
	}
	delete(d.nics, idx)
	// Its NTP and DNS servers and its MTU go with the lease.
	d.publishNTP()
	d.writeResolvers()
	if m, ok := d.mtus[idx]; ok {
		delete(d.mtus, idx)
		if m.set != m.orig {
			if err := dhcpSetMTU(link, m.orig); err != nil {
				d.log.Warn("failed to restore the MTU of an ended DHCP lease", "iface", ifName, "error", err)
			}
		}
	}
	removed := true
	for _, r := range a.routes {
		if err := dhcpRouteDel(r); err != nil && !errors.Is(err, syscall.ESRCH) {
			d.log.Warn("failed to remove a route of an ended DHCP lease", "iface", ifName, "route", routeDst(r), "error", err)
			d.stale = append(d.stale, dhcpApplied{link: link, routes: []*netlink.Route{r}})
			removed = false
		}
	}
	if err := dhcpAddrDel(link, a.addr); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
		d.log.Warn("failed to remove the address of an ended DHCP lease", "iface", ifName, "error", err)
		d.stale = append(d.stale, dhcpApplied{link: link, addr: a.addr})
		removed = false
	}
	if removed {
		d.log.Warn("DHCP lease ended (expired or refused), address removed until a new lease", "iface", ifName,
			"addr", a.addr.IPNet.String())
	}
}

// apply configures the address and default route of a lease on its NIC and
// removes what the previous lease of the NIC set and this one does not. The
// caller holds d.mu.
func (d *DHCPMode) apply(link netlink.Link, addr *netlink.Addr, lease *dhclient.Lease) error {
	if d.nics == nil {
		d.nics = map[int]dhcpApplied{}
	}
	idx := link.Attrs().Index
	prev, had := d.nics[idx]
	// Replace, so an address left by an earlier lease gets this mask.
	if err := dhcpAddrReplace(link, addr); err != nil {
		return fmt.Errorf("assign DHCP address: %w", err)
	}
	routes, err := d.applyRoutes(link, addr, lease, prev.routes)
	if err != nil {
		d.dropUnappliedAddr(link, addr, &prev, had)
		return err
	}
	if had && !prev.addr.IP.Equal(addr.IP) {
		if err := dhcpAddrDel(link, prev.addr); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
			d.log.Warn("failed to remove superseded DHCP address", "iface", link.Attrs().Name, "error", err)
			d.stale = append(d.stale, dhcpApplied{link: link, addr: prev.addr})
		}
	}
	d.nics[idx] = dhcpApplied{link: link, addr: addr, routes: routes, dns: leaseDNS(lease), ntp: leaseNTPServers(lease), domains: leaseSearchDomains(lease)}
	d.applyMTU(link, lease.MTU)
	d.writeResolvers()
	return nil
}

// leaseDNS returns the DNS servers of a lease (option 6).
func leaseDNS(lease *dhclient.Lease) []string {
	var out []string
	for _, ip := range lease.DNS {
		if ip.To4() != nil && !ip.IsUnspecified() {
			out = append(out, ip.String())
		}
	}
	return out
}

// applyMTU sets the lease MTU (option 26) on the NIC, like networkd with
// netplan's UseMTU=true. Teardown restores the MTU from before the first
// change. The caller holds d.mu.
func (d *DHCPMode) applyMTU(link netlink.Link, mtu uint16) {
	if mtu < dhcpMinMTU {
		return
	}
	idx := link.Attrs().Index
	m, ok := d.mtus[idx]
	if !ok {
		m = dhcpMTU{link: link, orig: link.Attrs().MTU, set: link.Attrs().MTU}
	}
	if m.set == int(mtu) {
		return
	}
	if err := dhcpSetMTU(link, int(mtu)); err != nil {
		d.log.Warn("failed to set the DHCP MTU", "iface", link.Attrs().Name, "mtu", mtu, "error", err)
		return
	}
	m.set = int(mtu)
	if d.mtus == nil {
		d.mtus = map[int]dhcpMTU{}
	}
	d.mtus[idx] = m
}

// writeResolvers writes the configured DNS servers and then those of the
// leases (by NIC index, without duplicates), and the lease domains as search
// domains, to resolv.conf when the content changed. The caller holds d.mu.
func (d *DHCPMode) writeResolvers() {
	if !d.manageDNS {
		return
	}
	servers := slices.Clone(d.resolvers)
	var search []string
	for _, idx := range slices.Sorted(maps.Keys(d.nics)) {
		n := d.nics[idx]
		for _, s := range n.dns {
			if !slices.Contains(servers, s) {
				servers = append(servers, s)
			}
		}
		for _, dom := range n.domains {
			if !slices.Contains(search, dom) {
				search = append(search, dom)
			}
		}
	}
	content := strings.Join(servers, ",") + " search " + strings.Join(search, " ")
	if len(servers) == 0 || content == d.written {
		return
	}
	if err := dhcpWriteResolvers(servers, search); err != nil {
		d.log.Warn("failed to write the DHCP DNS servers", "servers", servers, "search", search, "error", err)
		return
	}
	d.written = content
}

// applyRoutes adds the routes of a lease (leaseRoutes) next to the routes
// of other NICs, then removes the routes of the previous lease of this NIC
// that the lease does not have. Routes are appended, so a second NIC's
// default route does not replace the first; a route that is there already
// (a renewal) is EEXIST. When a route fails, the routes added here are
// removed again and the previous ones stay.
func (d *DHCPMode) applyRoutes(link netlink.Link, addr *netlink.Addr, lease *dhclient.Lease, prev []*netlink.Route) ([]*netlink.Route, error) {
	want := leaseRoutes(link, addr, lease)
	var added []*netlink.Route
	for _, r := range want {
		if err := dhcpRouteAdd(r); err != nil && !errors.Is(err, syscall.EEXIST) {
			for _, a := range added {
				if delErr := dhcpRouteDel(a); delErr != nil && !errors.Is(delErr, syscall.ESRCH) {
					d.stale = append(d.stale, dhcpApplied{link: link, routes: []*netlink.Route{a}})
				}
			}
			return nil, fmt.Errorf("add DHCP route %s via %s: %w", routeDst(r), r.Gw, err)
		}
		if !slices.ContainsFunc(prev, func(p *netlink.Route) bool { return sameRoute(p, r) }) {
			added = append(added, r)
		}
	}
	for _, p := range prev {
		if slices.ContainsFunc(want, func(r *netlink.Route) bool { return sameRoute(p, r) }) {
			continue
		}
		if err := dhcpRouteDel(p); err != nil && !errors.Is(err, syscall.ESRCH) {
			d.log.Warn("failed to remove an old DHCP route", "iface", link.Attrs().Name, "route", routeDst(p), "error", err)
			d.stale = append(d.stale, dhcpApplied{link: link, routes: []*netlink.Route{p}})
		}
	}
	return want, nil
}

func routeDst(r *netlink.Route) string {
	if r.Dst == nil {
		return "default"
	}
	return r.Dst.String()
}

// dropUnappliedAddr removes an address that was set before the route of its
// lease failed, unless it is the address of the previous lease; then the
// previous mask comes back if the lease changed it. The caller holds d.mu.
func (d *DHCPMode) dropUnappliedAddr(link netlink.Link, addr *netlink.Addr, prev *dhcpApplied, had bool) {
	if had && prev.addr.IP.Equal(addr.IP) {
		if prev.addr.IPNet.String() != addr.IPNet.String() {
			if err := dhcpAddrReplace(link, prev.addr); err != nil {
				d.log.Warn("failed to restore DHCP address", "iface", link.Attrs().Name, "error", err)
				prev.addr = addr // track what the kernel has
				d.nics[link.Attrs().Index] = *prev
			}
		}
		return
	}
	if err := dhcpAddrDel(link, addr); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
		d.log.Warn("failed to roll back DHCP address", "iface", link.Attrs().Name, "error", err)
		// Keep tracking it, so Teardown can still remove it.
		d.stale = append(d.stale, dhcpApplied{link: link, addr: addr})
	}
}

// leaseNTPServers returns the NTP servers (option 42) of a lease.
func leaseNTPServers(lease *dhclient.Lease) []string {
	var out []string
	for _, opt := range lease.OtherOptions {
		if opt.Type == layers.DHCPOptNTPServers {
			out = parseNTPServers(opt.Data)
		}
	}
	return out
}

// publishNTP publishes the NTP servers of all leases (by NIC index, each
// once) for the time sync, like networkd hands the servers of every link
// to timesyncd. The caller holds d.mu.
func (d *DHCPMode) publishNTP() {
	var servers []string
	for _, idx := range slices.Sorted(maps.Keys(d.nics)) {
		for _, s := range d.nics[idx].ntp {
			if !slices.Contains(servers, s) {
				servers = append(servers, s)
			}
		}
	}
	setDHCPNTPList(servers)
}

// leaseAddr builds the interface address from a lease. A lease without a
// subnet mask option gets the classful default mask, like other clients.
func leaseAddr(lease *dhclient.Lease) (*netlink.Addr, error) {
	ip := lease.FixedAddress.To4()
	if ip == nil || ip.IsUnspecified() {
		return nil, fmt.Errorf("lease has no IPv4 address")
	}
	mask := lease.Netmask
	if len(mask) == 0 {
		mask = ip.DefaultMask()
	}
	if _, bits := mask.Size(); bits != 8*net.IPv4len {
		return nil, fmt.Errorf("lease has an invalid subnet mask %v", mask)
	}
	return &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: mask}}, nil
}

// WaitForConnectivity polls the target URL until reachable or timeout.
func (d *DHCPMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

// Teardown stops the DHCP clients and removes the addresses and default
// routes that their leases configured, so a network retry starts clean.
func (d *DHCPMode) Teardown(_ context.Context) error {
	d.stopClients()
	d.mu.Lock()
	for _, t := range d.expiry {
		t.timer.Stop()
	}
	d.expiry = nil
	applied := d.stale
	for _, a := range d.nics {
		applied = append(applied, a)
	}
	mtus := d.mtus
	d.nics, d.stale, d.mtus, d.written = nil, nil, nil, ""
	setDHCPNTPList(nil)
	d.mu.Unlock()
	errs := restoreMTUs(mtus)
	for _, a := range applied {
		for _, r := range a.routes {
			if err := dhcpRouteDel(r); err != nil && !errors.Is(err, syscall.ESRCH) {
				errs = append(errs, fmt.Errorf("delete DHCP route %s: %w", routeDst(r), err))
			}
		}
		if a.addr == nil {
			continue
		}
		if err := dhcpAddrDel(a.link, a.addr); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
			errs = append(errs, fmt.Errorf("delete DHCP address: %w", err))
		}
	}
	return errors.Join(errs...)
}

// restoreMTUs sets the MTU from before the first lease again.
func restoreMTUs(mtus map[int]dhcpMTU) []error {
	var errs []error
	for _, m := range mtus {
		if m.set == m.orig {
			continue
		}
		if err := dhcpSetMTU(m.link, m.orig); err != nil {
			errs = append(errs, fmt.Errorf("restore MTU %d on %s: %w", m.orig, m.link.Attrs().Name, err))
		}
	}
	return errs
}

// physicalInterfaces returns non-loopback, non-virtual interfaces.
func physicalInterfaces(ctx context.Context) ([]net.Interface, error) {
	nicNames, err := DetectPhysicalNICsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("detect physical nics: %w", err)
	}

	result := make([]net.Interface, 0, len(nicNames))
	for _, name := range nicNames {
		iface, ifErr := net.InterfaceByName(name)
		if ifErr != nil {
			slog.Debug("skipping NIC missing during DHCP scan", "interface", name, "error", ifErr)
			continue
		}
		result = append(result, *iface)
	}
	return result, nil
}
