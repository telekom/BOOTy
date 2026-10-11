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
)

// DHCPMode implements the Mode interface using DHCP on all physical interfaces.
type DHCPMode struct {
	client *dhclient.Client
	log    *slog.Logger
}

// NewDHCPMode creates a DHCPMode with a component logger.
func NewDHCPMode() *DHCPMode {
	return &DHCPMode{
		log: slog.Default().With("component", "dhcp"),
	}
}

// dhcpResult carries the outcome of a single NIC probe.
type dhcpResult struct {
	client *dhclient.Client
	iface  string
}

// Setup tries DHCP on all physical interfaces in parallel.
// Each NIC gets its own 15 s timeout; the first lease wins.
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

	d.log.Info("Probing DHCP on all interfaces in parallel", "count", len(ifaces))

	probeCtx, probeCancel := context.WithCancel(ctx)
	defer probeCancel()

	results := make(chan dhcpResult, len(ifaces))
	var wg sync.WaitGroup
	var winner dhcpWinner

	for i := range ifaces {
		wg.Add(1)
		go func(iface net.Interface) {
			defer wg.Done()
			d.probeNIC(probeCtx, iface, results, &winner)
		}(ifaces[i])
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		d.client = res.client
		d.log.Info("DHCP succeeded", "interface", res.iface)
		probeCancel()
		break
	}

	if d.client == nil {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("DHCP probing canceled: %w", err)
		}
		return fmt.Errorf("DHCP failed on all %d interfaces", len(ifaces))
	}
	return nil
}

// probeNIC attempts DHCP on a single interface with a 15 s timeout.
// On success it sends a dhcpResult to the results channel.
func (d *DHCPMode) probeNIC(ctx context.Context, iface net.Interface, results chan<- dhcpResult, winner *dhcpWinner) {
	d.log.Info("Attempting DHCP", "interface", iface.Name)

	link, err := netlink.LinkByName(iface.Name)
	if err != nil {
		d.log.Warn("Cannot find link for DHCP", "interface", iface.Name, "error", err)
		return
	}
	if err := netlink.LinkSetUp(link); err != nil {
		d.log.Warn("Cannot bring up link for DHCP", "interface", iface.Name, "error", err)
		return
	}

	leased := make(chan struct{}, 1)
	hostname, _ := os.Hostname()
	client := newDHCPClient(&iface, hostname, d.onBound(link, iface.Name, leased, winner))
	client.Start()

	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()

	select {
	case <-leased:
		results <- dhcpResult{client: client, iface: iface.Name}
	case <-timer.C:
		client.Stop()
		d.log.Info("DHCP timeout on interface", "interface", iface.Name)
	case <-ctx.Done():
		client.Stop()
		d.log.Info("DHCP probe canceled", "interface", iface.Name, "error", ctx.Err())
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
	dhcpAddrAdd      = netlink.AddrAdd
	dhcpRouteReplace = netlink.RouteReplace
)

// onBound returns a callback that configures the interface address and default route.
// The winner ensures only the first NIC whose lease applies cleanly configures the host;
// later leases are ignored. Callbacks wait for an apply in progress, so a lease on another
// NIC is still used when the first apply fails.
// An address that already exists (for example after a network retry) counts as applied.
func (d *DHCPMode) onBound(link netlink.Link, ifName string, leased chan<- struct{}, winner *dhcpWinner) func(*dhclient.Lease) {
	return func(lease *dhclient.Lease) {
		// Validate before claiming the winner, so an unusable lease on one
		// NIC does not block a usable lease on another NIC.
		addr, err := leaseAddr(lease)
		if err != nil {
			d.log.Warn("Ignoring DHCP lease", "iface", ifName, "error", err)
			return
		}
		winner.mu.Lock()
		defer winner.mu.Unlock()
		if winner.won {
			return
		}
		if err := dhcpAddrAdd(link, addr); err != nil && !errors.Is(err, syscall.EEXIST) {
			d.log.Warn("Failed to assign DHCP address", "iface", ifName, "error", err)
			return
		}
		winner.won = true
		d.log.Info("DHCP lease obtained", "iface", ifName, "addr", addr.IPNet.String())
		if len(lease.Router) > 0 {
			// Replace, so a default route left by an earlier attempt or
			// another NIC does not stay in place.
			route := &netlink.Route{Gw: lease.Router[0], LinkIndex: link.Attrs().Index}
			if err := dhcpRouteReplace(route); err != nil {
				d.log.Warn("failed to set default route", "gw", lease.Router[0], "error", err)
			}
		}
		select {
		case leased <- struct{}{}:
		default:
		}
	}
}

// dhcpWinner lets only the first cleanly applied lease configure the host.
type dhcpWinner struct {
	mu  sync.Mutex
	won bool
}

// leaseAddr builds the interface address from a lease. A lease without a
// subnet mask option gets the classful default mask, like other clients.
func leaseAddr(lease *dhclient.Lease) (*netlink.Addr, error) {
	ip := lease.FixedAddress.To4()
	if ip == nil {
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

// Teardown stops the DHCP client.
func (d *DHCPMode) Teardown(_ context.Context) error {
	if d.client != nil {
		d.client.Stop()
	}
	return nil
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
