//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/digineo/go-dhclient"
	"github.com/google/gopacket/layers"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// DHCPMode implements the Mode interface with DHCPv4 on every physical
// interface, like systemd-networkd with the legacy ramdisk's netplan
// ("dhcp4: true" on every en* and eth* NIC): a client runs on every NIC for
// the whole run, every lease configures its NIC (address and a default route
// with the netplan metric), and renewals update it.
type DHCPMode struct {
	log *slog.Logger

	mu      sync.Mutex
	clients []*dhclient.Client
	nics    map[int]dhcpApplied // by link index: what the current lease configured
	stale   []dhcpApplied       // state that could not be removed; Teardown retries
}

// dhcpApplied is the address and default route a lease configured.
type dhcpApplied struct {
	link  netlink.Link
	addr  *netlink.Addr
	route *netlink.Route
}

// dhcpRouteMetric is the metric of DHCP default routes: netplan writes
// RouteMetric=100 for ethernets, so the routes of all NICs are equal.
const dhcpRouteMetric = 100

// dhcpSetupTimeout bounds the wait for the first lease in Setup.
const dhcpSetupTimeout = 15 * time.Second

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
func (d *DHCPMode) Setup(ctx context.Context, _ *Config) error {
	if d.log == nil {
		d.log = slog.Default().With("component", "dhcp")
	}
	ifaces, err := physicalInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("listing interfaces: %w", err)
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no physical interfaces found for DHCP")
	}

	d.log.Info("Starting DHCP on all interfaces", "count", len(ifaces))
	leased := make(chan string, len(ifaces))
	hostname, _ := os.Hostname()
	for i := range ifaces {
		iface := ifaces[i]
		link, err := netlink.LinkByName(iface.Name)
		if err != nil {
			d.log.Warn("Cannot find link for DHCP", "interface", iface.Name, "error", err)
			continue
		}
		if err := netlink.LinkSetUp(link); err != nil {
			d.log.Warn("Cannot bring up link for DHCP", "interface", iface.Name, "error", err)
			continue
		}
		client := newDHCPClient(&iface, hostname, d.onBound(link, iface.Name, leased))
		client.Start()
		d.mu.Lock()
		d.clients = append(d.clients, client)
		d.mu.Unlock()
	}

	timer := time.NewTimer(dhcpSetupTimeout)
	defer timer.Stop()
	select {
	case name := <-leased:
		d.log.Info("DHCP succeeded", "interface", name)
		return nil
	case <-timer.C:
		d.stopClients()
		return fmt.Errorf("DHCP failed on all %d interfaces", len(ifaces))
	case <-ctx.Done():
		d.stopClients()
		return fmt.Errorf("DHCP probing canceled: %w", ctx.Err())
	}
}

// stopClients stops every DHCP client.
func (d *DHCPMode) stopClients() {
	d.mu.Lock()
	clients := d.clients
	d.clients = nil
	d.mu.Unlock()
	for _, c := range clients {
		c.Stop()
	}
}

// newDHCPClient builds a client that requests the default parameters
// (subnet mask, router, DNS, MTU and more) and sends the hostname.
// dhclient only adds the parameter request list when no option is set, so
// adding the hostname alone would drop it.
func newDHCPClient(iface *net.Interface, hostname string, onBound dhclient.Callback) *dhclient.Client {
	client := &dhclient.Client{Iface: iface, OnBound: onBound}
	for _, param := range dhclient.DefaultParamsRequestList {
		client.AddParamRequest(param)
	}
	if hostname != "" {
		client.AddOption(layers.DHCPOptHostname, []byte(hostname))
	}
	return client
}

// Netlink seams for onBound tests.
var (
	dhcpAddrReplace = replaceLeaseAddr
	dhcpAddrDel     = netlink.AddrDel
	dhcpRouteAdd    = netlink.RouteAppend
	dhcpRouteDel    = netlink.RouteDel
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
		if err := d.apply(link, addr, lease); err != nil {
			d.log.Warn("failed to apply DHCP lease", "iface", ifName, "error", err)
			return
		}
		recordLeaseNTPServers(lease)
		d.log.Info("DHCP lease applied", "iface", ifName, "addr", addr.IPNet.String())
		select {
		case leased <- ifName:
		default:
		}
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
	route, err := d.applyRoute(link, addr, lease, prev.route)
	if err != nil {
		d.dropUnappliedAddr(link, addr, prev, had)
		return err
	}
	if had && !prev.addr.IP.Equal(addr.IP) {
		if err := dhcpAddrDel(link, prev.addr); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
			d.log.Warn("failed to remove superseded DHCP address", "iface", link.Attrs().Name, "error", err)
			d.stale = append(d.stale, dhcpApplied{link: link, addr: prev.addr})
		}
	}
	d.nics[idx] = dhcpApplied{link: link, addr: addr, route: route}
	return nil
}

// applyRoute adds the default route of a lease (proto dhcp, the lease
// address as source, metric 100, like networkd) next to the default routes
// of other NICs, then removes the previous route of this NIC. A lease
// without a router option removes it (RFC 2131 4.4.5).
func (d *DHCPMode) applyRoute(link netlink.Link, addr *netlink.Addr, lease *dhclient.Lease, prev *netlink.Route) (*netlink.Route, error) {
	var route *netlink.Route
	if len(lease.Router) > 0 {
		route = &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Gw:        lease.Router[0],
			Src:       addr.IP,
			Priority:  dhcpRouteMetric,
			Protocol:  unix.RTPROT_DHCP,
		}
		// Append: a second NIC's default route must not replace the first.
		// The same route again (a renewal) is EEXIST.
		if err := dhcpRouteAdd(route); err != nil && !errors.Is(err, syscall.EEXIST) {
			return nil, fmt.Errorf("set default route via %s: %w", lease.Router[0], err)
		}
	}
	if prev != nil && (route == nil || !prev.Gw.Equal(route.Gw) || !prev.Src.Equal(route.Src)) {
		if err := dhcpRouteDel(prev); err != nil && !errors.Is(err, syscall.ESRCH) {
			d.log.Warn("failed to remove the old DHCP default route", "iface", link.Attrs().Name, "error", err)
			d.stale = append(d.stale, dhcpApplied{link: link, route: prev})
		}
	}
	return route, nil
}

// dropUnappliedAddr removes an address that was set before the route of its
// lease failed, unless it is the address of the previous lease; then the
// previous mask comes back if the lease changed it. The caller holds d.mu.
func (d *DHCPMode) dropUnappliedAddr(link netlink.Link, addr *netlink.Addr, prev dhcpApplied, had bool) {
	if had && prev.addr.IP.Equal(addr.IP) {
		if prev.addr.IPNet.String() != addr.IPNet.String() {
			if err := dhcpAddrReplace(link, prev.addr); err != nil {
				d.log.Warn("failed to restore DHCP address", "iface", link.Attrs().Name, "error", err)
				prev.addr = addr // track what the kernel has
				d.nics[link.Attrs().Index] = prev
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

// recordLeaseNTPServers keeps the NTP servers (option 42) of the applied
// lease for the time sync.
func recordLeaseNTPServers(lease *dhclient.Lease) {
	var data []byte
	for _, opt := range lease.OtherOptions {
		if opt.Type == layers.DHCPOptNTPServers {
			data = opt.Data
		}
	}
	setDHCPNTPServers(data)
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
	applied := d.stale
	for _, a := range d.nics {
		applied = append(applied, a)
	}
	d.nics, d.stale = nil, nil
	d.mu.Unlock()
	var errs []error
	for _, a := range applied {
		if a.route != nil {
			if err := dhcpRouteDel(a.route); err != nil && !errors.Is(err, syscall.ESRCH) {
				errs = append(errs, fmt.Errorf("delete DHCP default route: %w", err))
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
