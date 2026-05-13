//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/vishvananda/netlink"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	// vxlanOverhead is the VXLAN encapsulation overhead in bytes:
	// 8 (outer UDP) + 8 (VXLAN header) + 20 (outer IPv4) + 14 (outer Ethernet).
	vxlanOverhead = 50

	// vxlanPort is the IANA-assigned VXLAN UDP port.
	vxlanPort = 4789

	// defaultMTU is the fallback inner MTU when cfg.MTU is too low.
	defaultMTU = 1500

	// asnMax2Byte is the maximum 2-byte ASN value, used to select
	// between 2-octet and 4-octet BGP community / RD formats.
	asnMax2Byte = 65535
)

// fdbInstaller abstracts netlink FDB operations for testability.
type fdbInstaller interface {
	LinkByName(name string) (netlink.Link, error)
	NeighSet(neigh *netlink.Neigh) error
	NeighAppend(neigh *netlink.Neigh) error
	NeighDel(neigh *netlink.Neigh) error
}

// netlinkFDB is the production implementation using real netlink calls.
type netlinkFDB struct{}

func (netlinkFDB) LinkByName(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("link by name %s: %w", name, err)
	}
	return link, nil
}

func (netlinkFDB) NeighSet(n *netlink.Neigh) error {
	if err := netlink.NeighSet(n); err != nil {
		return fmt.Errorf("neigh set: %w", err)
	}
	return nil
}

func (netlinkFDB) NeighAppend(n *netlink.Neigh) error {
	if err := netlink.NeighAppend(n); err != nil {
		return fmt.Errorf("neigh append: %w", err)
	}
	return nil
}

func (netlinkFDB) NeighDel(n *netlink.Neigh) error {
	if err := netlink.NeighDel(n); err != nil {
		return fmt.Errorf("neigh del: %w", err)
	}
	return nil
}

// OverlayTier manages EVPN Type-5 routes and VXLAN encapsulation.
// When EnableL2 is set, it also handles Type-2 (MAC/IP) and Type-3
// (Inclusive Multicast) routes for L2 overlay use cases.
type OverlayTier struct {
	bgp    *server.BgpServer
	cfg    *Config
	log    *slog.Logger
	cancel context.CancelFunc
	fdb    fdbInstaller

	// Track resources created by us for clean teardown.
	createdVRF      bool
	createdBridge   bool
	createdVXLAN    bool
	addedLoopbackIP *netlink.Addr
	gatewayFDB      *netlink.Neigh

	// macVTEP tracks MAC → VTEP mappings learned from Type-2 routes
	// so withdrawals (which lack next-hop) can still delete the right
	// FDB entry. WatchEvent callbacks may run concurrently.
	macVTEP sync.Map
}

// NewOverlayTier creates a new overlay tier.
func NewOverlayTier(cfg *Config) *OverlayTier {
	return &OverlayTier{
		cfg: cfg,
		log: slog.With("tier", "overlay"),
		fdb: netlinkFDB{},
	}
}

// SetBgpServer sets the shared BGP server from the underlay tier.
func (o *OverlayTier) SetBgpServer(s *server.BgpServer) {
	o.bgp = s
}

// Setup creates VXLAN, bridge, and advertises the provision subnet as an
// EVPN Type-5 (IP Prefix) route so the fabric can route to this node.
// Incoming Type-5 routes from the fabric are installed as kernel routes
// by watchRoutes. VRF creation is handled by the stack before setup.
func (o *OverlayTier) Setup(ctx context.Context) error {
	switch OverlayType(o.cfg.OverlayType) {
	case OverlayNone:
		o.log.Info("overlay type is none, skipping overlay setup")
		return nil
	case OverlayL3VPN:
		return fmt.Errorf("overlay type %q is not yet implemented", o.cfg.OverlayType)
	case OverlayEVPNVXLAN:
		// default — continue with EVPN-VXLAN setup below
	default:
		return fmt.Errorf("unknown overlay type %q", o.cfg.OverlayType)
	}

	if o.bgp == nil {
		return fmt.Errorf("BGP server not set: call SetBgpServer before Setup")
	}

	if err := o.createVXLANAndBridge(); err != nil {
		return fmt.Errorf("create VXLAN/bridge: %w", err)
	}

	if err := o.addProvisionIP(); err != nil {
		return fmt.Errorf("add provision IP: %w", err)
	}

	if err := o.addOverlayLoopback(); err != nil {
		return fmt.Errorf("add overlay loopback: %w", err)
	}

	if err := o.advertiseType5(ctx); err != nil {
		return fmt.Errorf("advertise EVPN Type-5: %w", err)
	}

	if o.cfg.EnableL2 {
		if err := o.advertiseType3(ctx); err != nil {
			return fmt.Errorf("advertise EVPN Type-3: %w", err)
		}
		if err := o.advertiseType2(ctx); err != nil {
			return fmt.Errorf("advertise EVPN Type-2: %w", err)
		}
	}

	watchCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	go o.watchRoutes(watchCtx)

	return nil
}

// Ready waits until the overlay is operational by checking EVPN route state.
func (o *OverlayTier) Ready(_ context.Context, _ time.Duration) error {
	// Overlay is ready once Setup completes (routes are advertised synchronously).
	return nil
}

// Teardown removes the overlay network resources we created (bridge, vxlan)
// and removes the overlay loopback IP from lo.
// VRF is cleaned up separately by Stack.Teardown after underlay detaches.
func (o *OverlayTier) Teardown(_ context.Context) error {
	if o.cancel != nil {
		o.cancel()
	}

	if o.gatewayFDB != nil {
		if err := o.fdb.NeighDel(o.gatewayFDB); err != nil {
			o.log.Debug("failed to remove gateway BUM FDB entry", "vtep", o.gatewayFDB.IP, "error", err)
		} else {
			o.log.Info("removed gateway BUM FDB entry", "vtep", o.gatewayFDB.IP)
		}
		o.gatewayFDB = nil
	}

	// Remove overlay loopback IP from lo.
	if o.addedLoopbackIP != nil {
		lo, err := netlink.LinkByName("lo")
		if err == nil {
			if err := netlink.AddrDel(lo, o.addedLoopbackIP); err != nil {
				o.log.Warn("failed to remove overlay loopback IP", "ip", o.addedLoopbackIP, "error", err)
			}
		}
	}

	vxlanName := o.vxlanName()

	type owned struct {
		name    string
		created bool
	}
	for _, res := range []owned{
		{o.cfg.BridgeName, o.createdBridge},
		{vxlanName, o.createdVXLAN},
	} {
		if res.name == "" || !res.created {
			continue
		}
		link, err := netlink.LinkByName(res.name)
		if err != nil {
			continue
		}
		if err := netlink.LinkDel(link); err != nil {
			o.log.Warn("failed to remove interface", "name", res.name, "error", err)
		}
	}

	return nil
}

// CreateVRF creates the VRF interface if VRFName is configured.
// Called by Stack before underlay setup so that dummy/NICs can be assigned.
func (o *OverlayTier) CreateVRF() error {
	if o.cfg.VRFName == "" {
		return nil
	}

	vrf := &netlink.Vrf{
		LinkAttrs: netlink.LinkAttrs{Name: o.cfg.VRFName},
		Table:     o.cfg.VRFTableID,
	}
	if err := netlink.LinkAdd(vrf); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("add VRF %s: %w", o.cfg.VRFName, err)
		}
	} else {
		o.createdVRF = true
	}

	link, err := netlink.LinkByName(o.cfg.VRFName)
	if err != nil {
		return fmt.Errorf("find VRF %s: %w", o.cfg.VRFName, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up VRF %s: %w", o.cfg.VRFName, err)
	}

	o.log.Info("vrf ready", "name", o.cfg.VRFName, "table", o.cfg.VRFTableID)
	return nil
}

// vxlanName returns the VXLAN interface name derived from the provision VNI.
func (o *OverlayTier) vxlanName() string {
	return fmt.Sprintf("vx%d", o.cfg.ProvisionVNI)
}

func (o *OverlayTier) createVXLANAndBridge() error {
	vxlanName := o.vxlanName()

	vxLink, err := o.createVXLAN(vxlanName)
	if err != nil {
		return err
	}

	brLink, err := o.createBridge()
	if err != nil {
		return err
	}

	if err := netlink.LinkSetMasterByIndex(vxLink, brLink.Attrs().Index); err != nil {
		return fmt.Errorf("attach VXLAN to bridge: %w", err)
	}

	// Assign bridge to VRF for traffic isolation.
	if o.cfg.VRFName != "" {
		vrfLink, err := netlink.LinkByName(o.cfg.VRFName)
		if err != nil {
			return fmt.Errorf("find VRF %s: %w", o.cfg.VRFName, err)
		}
		if err := netlink.LinkSetMasterByIndex(brLink, vrfLink.Attrs().Index); err != nil {
			return fmt.Errorf("assign bridge to VRF: %w", err)
		}
	}

	if err := netlink.LinkSetUp(brLink); err != nil {
		return fmt.Errorf("bring up bridge: %w", err)
	}
	if err := netlink.LinkSetUp(vxLink); err != nil {
		return fmt.Errorf("bring up VXLAN: %w", err)
	}

	// Install a BUM FDB entry so broadcast/unknown/multicast traffic
	// (e.g. ARP for the gateway) is flooded to the gateway VTEP.
	// Without this, the VXLAN FDB is empty and BUM frames are dropped.
	if o.cfg.ProvisionGateway != "" {
		if err := o.addGatewayFDB(vxLink); err != nil {
			o.log.Warn("gateway BUM FDB entry failed (non-fatal)", "error", err)
		}
	}

	o.log.Info("created VXLAN and bridge",
		"vxlan", vxlanName, "vni", o.cfg.ProvisionVNI,
		"bridge", o.cfg.BridgeName,
	)
	return nil
}

func (o *OverlayTier) createVXLAN(name string) (netlink.Link, error) {
	vxlanMTU := o.cfg.MTU - vxlanOverhead
	if vxlanMTU <= 0 {
		vxlanMTU = defaultMTU
	}

	srcAddr := net.ParseIP(o.cfg.RouterID)
	vxlan := &netlink.Vxlan{
		LinkAttrs:    netlink.LinkAttrs{Name: name},
		VxlanId:      o.cfg.ProvisionVNI,
		SrcAddr:      srcAddr,
		Port:         vxlanPort,
		Learning:     false,
		VtepDevIndex: 0,
	}

	if err := netlink.LinkAdd(vxlan); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("add VXLAN %s: %w", name, err)
		}
	} else {
		o.createdVXLAN = true
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("find VXLAN: %w", err)
	}
	if err := netlink.LinkSetMTU(link, vxlanMTU); err != nil {
		return nil, fmt.Errorf("set VXLAN MTU: %w", err)
	}
	return link, nil
}

func (o *OverlayTier) createBridge() (netlink.Link, error) {
	hwAddr, err := net.ParseMAC(o.cfg.BridgeMAC)
	if err != nil {
		return nil, fmt.Errorf("parse bridge MAC %s: %w", o.cfg.BridgeMAC, err)
	}

	bridge := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name:         o.cfg.BridgeName,
			HardwareAddr: hwAddr,
		},
	}
	if err := netlink.LinkAdd(bridge); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("add bridge %s: %w", o.cfg.BridgeName, err)
		}
	} else {
		o.createdBridge = true
	}

	link, err := netlink.LinkByName(o.cfg.BridgeName)
	if err != nil {
		return nil, fmt.Errorf("find bridge: %w", err)
	}
	return link, nil
}

func (o *OverlayTier) addProvisionIP() error {
	if o.cfg.ProvisionIP == "" {
		return nil
	}

	link, err := netlink.LinkByName(o.cfg.BridgeName)
	if err != nil {
		return fmt.Errorf("find bridge %s: %w", o.cfg.BridgeName, err)
	}

	addr, err := netlink.ParseAddr(o.cfg.ProvisionIP)
	if err != nil {
		return fmt.Errorf("parse provision IP %s: %w", o.cfg.ProvisionIP, err)
	}

	if err := netlink.AddrAdd(link, addr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add provision IP to bridge: %w", err)
	}

	o.log.Info("assigned provision IP", "bridge", o.cfg.BridgeName, "ip", o.cfg.ProvisionIP)
	return nil
}

// addGatewayFDB installs a BUM (broadcast/unknown/multicast) FDB entry on the
// VXLAN interface pointing to the gateway's VTEP. This is equivalent to:
//
//	bridge fdb append 00:00:00:00:00:00 dev vxlanXXX dst <gateway> self permanent
//
// Without this entry the VXLAN has no remote VTEP and drops all BUM frames,
// making ARP resolution impossible.
func (o *OverlayTier) addGatewayFDB(vxLink netlink.Link) error {
	gwIP := net.ParseIP(o.cfg.ProvisionGateway)
	if gwIP == nil {
		return fmt.Errorf("parse gateway VTEP IP %q", o.cfg.ProvisionGateway)
	}

	fdb := &netlink.Neigh{
		LinkIndex:    vxLink.Attrs().Index,
		Family:       syscall.AF_BRIDGE,
		HardwareAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0},
		IP:           gwIP,
		Flags:        netlink.NTF_SELF,
		State:        netlink.NUD_PERMANENT,
	}
	if err := o.fdb.NeighAppend(fdb); err != nil {
		return fmt.Errorf("append BUM FDB entry for %s: %w", o.cfg.ProvisionGateway, err)
	}
	o.gatewayFDB = fdb

	o.log.Info("installed gateway BUM FDB entry", "vxlan", vxLink.Attrs().Name, "vtep", o.cfg.ProvisionGateway)
	return nil
}

func (o *OverlayTier) addOverlayLoopback() error {
	if o.cfg.OverlayIP == "" {
		return nil
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("find loopback: %w", err)
	}

	// Try /128 (IPv6) first, fall back to /32 (IPv4).
	addr, err := netlink.ParseAddr(o.cfg.OverlayIP + "/128")
	if err != nil {
		addr, err = netlink.ParseAddr(o.cfg.OverlayIP + "/32")
		if err != nil {
			return fmt.Errorf("parse overlay IP %s: %w", o.cfg.OverlayIP, err)
		}
	}

	if err := netlink.AddrAdd(lo, addr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add overlay IP to loopback: %w", err)
	}
	o.addedLoopbackIP = addr

	o.log.Info("added overlay loopback", "ip", o.cfg.OverlayIP)
	return nil
}

// advertiseType5 advertises this node's provision host IP as an EVPN Type-5
// (IP Prefix) /32 route so the fabric can route overlay traffic to this VTEP.
// The /32 is required because multiple BOOTy nodes may share the same /24
// provision subnet — only unique host routes allow per-node reachability.
func (o *OverlayTier) advertiseType5(ctx context.Context) error {
	if o.cfg.ProvisionIP == "" {
		o.log.Warn("provision IP not set, skipping EVPN Type-5 advertisement")
		return nil
	}

	rd, err := buildRouteDistinguisher(o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build route distinguisher: %w", err)
	}

	// Extract the host IP from the CIDR and advertise it as a /32 host route.
	ip, _, err := net.ParseCIDR(o.cfg.ProvisionIP)
	if err != nil {
		return fmt.Errorf("parse provision IP %s: %w", o.cfg.ProvisionIP, err)
	}
	hostRoute := ip.String() + "/32"

	nlri, err := buildEVPNType5NLRI(rd, hostRoute, o.cfg.RouterID, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build EVPN NLRI: %w", err)
	}

	pattrs, err := buildType5PathAttrs(nlri, o.cfg.RouterID, o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build path attributes: %w", err)
	}

	_, err = o.bgp.AddPath(ctx, &apipb.AddPathRequest{
		Path: &apipb.Path{
			Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
			Nlri:   nlri,
			Pattrs: pattrs,
		},
	})
	if err != nil {
		return fmt.Errorf("add EVPN Type-5 path: %w", err)
	}

	o.log.Info("advertised EVPN type-5 host route",
		"ip", hostRoute, "vni", o.cfg.ProvisionVNI)
	return nil
}

// advertiseType3 originates an EVPN Type-3 (Inclusive Multicast Ethernet Tag)
// route so that remote VTEPs in the fabric include this node as a BUM flooding
// target. The IMET route carries a PMSI tunnel attribute (ingress replication)
// telling peers to use unicast VXLAN encapsulation for BUM traffic.
func (o *OverlayTier) advertiseType3(ctx context.Context) error {
	rd, err := buildRouteDistinguisher(o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build route distinguisher: %w", err)
	}

	nlri, err := buildEVPNType3NLRI(rd, o.cfg.RouterID)
	if err != nil {
		return fmt.Errorf("build EVPN type-3 NLRI: %w", err)
	}

	pattrs, err := buildType3PathAttrs(nlri, o.cfg.RouterID, o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build type-3 path attributes: %w", err)
	}

	_, err = o.bgp.AddPath(ctx, &apipb.AddPathRequest{
		Path: &apipb.Path{
			Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
			Nlri:   nlri,
			Pattrs: pattrs,
		},
	})
	if err != nil {
		return fmt.Errorf("add EVPN type-3 path: %w", err)
	}

	o.log.Info("advertised EVPN type-3 IMET route",
		"vtep", o.cfg.RouterID, "vni", o.cfg.ProvisionVNI)
	return nil
}

// advertiseType2 originates an EVPN Type-2 (MAC/IP Advertisement) route for
// the local bridge MAC and provision IP. This lets remote VTEPs install an FDB
// entry via BGP control-plane learning instead of relying on data-plane flooding.
func (o *OverlayTier) advertiseType2(ctx context.Context) error {
	if o.cfg.BridgeMAC == "" || o.cfg.ProvisionIP == "" {
		o.log.Warn("bridge MAC or provision IP not set, skipping type-2 advertisement")
		return nil
	}

	if _, err := net.ParseMAC(o.cfg.BridgeMAC); err != nil {
		return fmt.Errorf("parse bridge MAC %s: %w", o.cfg.BridgeMAC, err)
	}

	ip, _, err := net.ParseCIDR(o.cfg.ProvisionIP)
	if err != nil {
		return fmt.Errorf("parse provision IP %s: %w", o.cfg.ProvisionIP, err)
	}

	rd, err := buildRouteDistinguisher(o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build route distinguisher: %w", err)
	}

	nlri, err := buildEVPNType2NLRI(rd, o.cfg.BridgeMAC, ip.String(), uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build EVPN type-2 NLRI: %w", err)
	}

	pattrs, err := buildType2PathAttrs(nlri, o.cfg.RouterID, o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build type-2 path attributes: %w", err)
	}

	_, err = o.bgp.AddPath(ctx, &apipb.AddPathRequest{
		Path: &apipb.Path{
			Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
			Nlri:   nlri,
			Pattrs: pattrs,
		},
	})
	if err != nil {
		return fmt.Errorf("add EVPN type-2 path: %w", err)
	}

	o.log.Info("advertised EVPN type-2 MAC/IP route",
		"mac", o.cfg.BridgeMAC, "ip", ip, "vni", o.cfg.ProvisionVNI)
	return nil
}

func (o *OverlayTier) watchRoutes(ctx context.Context) {
	err := o.bgp.WatchEvent(ctx, &apipb.WatchEventRequest{
		Table: &apipb.WatchEventRequest_Table{
			Filters: []*apipb.WatchEventRequest_Table_Filter{
				{
					Type: apipb.WatchEventRequest_Table_Filter_BEST,
					Init: true,
				},
			},
		},
	}, func(resp *apipb.WatchEventResponse) {
		if t := resp.GetTable(); t != nil {
			for _, p := range t.GetPaths() {
				o.processRouteUpdate(p)
			}
		}
	})
	if err != nil {
		o.log.Warn("route watcher stopped", "error", err)
	}
}

// processRouteUpdate handles a single BGP path update by dispatching to the
// appropriate handler based on NLRI type. Routes whose extended communities do
// not carry a Route Target matching the local ASN+VNI are silently skipped so
// that foreign-tenant routes are never installed into the kernel.
func (o *OverlayTier) processRouteUpdate(p *apipb.Path) {
	withdraw := p.GetIsWithdraw()
	action := "add"
	if withdraw {
		action = "withdraw"
	}

	nlri := p.GetNlri()
	if nlri == nil {
		return
	}

	if !p.GetIsWithdraw() && !matchesLocalRT(p, o.cfg.ASN, uint32(o.cfg.ProvisionVNI)) {
		o.log.Debug("route update skipped: RT mismatch", "action", action, "type", nlri.GetTypeUrl())
		return
	}

	msg, err := nlri.UnmarshalNew()
	if err != nil {
		o.log.Debug("route update unmarshal failed", "error", err)
		return
	}

	vtep := extractNextHop(p)

	switch route := msg.(type) {
	case *apipb.EVPNIPPrefixRoute:
		o.handleType5Route(route, vtep, withdraw)
	case *apipb.EVPNMACIPAdvertisementRoute:
		if o.cfg.EnableL2 {
			o.handleType2Route(route, vtep, withdraw)
		}
	case *apipb.EVPNInclusiveMulticastEthernetTagRoute:
		if o.cfg.EnableL2 {
			o.handleType3Route(route, vtep, withdraw)
		}
	default:
		o.log.Debug("route update", "action", action, "type", nlri.GetTypeUrl())
	}
}

// handleType5Route installs or removes a kernel route for an IP prefix
// received via EVPN Type-5 (IP Prefix) route. This is how BOOTy learns
// the default route (and any other prefixes) from the fabric.
func (o *OverlayTier) handleType5Route(route *apipb.EVPNIPPrefixRoute, vtep string, withdraw bool) {
	// Skip routes originated by this node (e.g., reflected back by the RR).
	// Installing our own route would override the connected route and break
	// provisioning connectivity.
	if vtep == o.cfg.RouterID {
		return
	}

	prefix := route.GetIpPrefix()
	prefixLen := route.GetIpPrefixLen()

	dst, err := parsePrefixRoute(prefix, prefixLen)
	if err != nil {
		o.log.Debug("type-5 route with invalid prefix", "prefix", prefix, "len", prefixLen, "error", err)
		return
	}

	// Resolve the gateway: prefer the NLRI's GwAddress, fall back to next-hop.
	gwStr := route.GetGwAddress()
	if gwStr == "" || gwStr == "0.0.0.0" {
		gwStr = vtep
	}
	gw := net.ParseIP(gwStr)
	if gw == nil {
		o.log.Debug("type-5 route with no valid gateway", "prefix", dst, "gw", gwStr)
		return
	}

	link, err := o.fdb.LinkByName(o.cfg.BridgeName)
	if err != nil {
		o.log.Warn("cannot find bridge for route install", "bridge", o.cfg.BridgeName, "error", err)
		return
	}

	kr := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dst,
		Gw:        gw,
	}

	if withdraw {
		if err := netlink.RouteDel(kr); err != nil {
			o.log.Debug("failed to delete route from type-5 withdraw", "dst", dst, "gw", gw, "error", err)
		} else {
			o.log.Info("removed route from type-5 withdraw", "dst", dst, "gw", gw)
		}
		return
	}

	if err := netlink.RouteReplace(kr); err != nil {
		o.log.Warn("failed to install route from type-5", "dst", dst, "gw", gw, "error", err)
	} else {
		o.log.Info("installed route from type-5", "dst", dst, "gw", gw)
	}
}

// handleType2Route installs or removes an FDB entry for a remote MAC learned
// via EVPN Type-2 (MAC/IP Advertisement) route. Only active when EnableL2 is set.
func (o *OverlayTier) handleType2Route(route *apipb.EVPNMACIPAdvertisementRoute, vtep string, withdraw bool) {
	mac, err := net.ParseMAC(route.GetMacAddress())
	if err != nil {
		o.log.Debug("type-2 route with invalid MAC", "mac", route.GetMacAddress(), "error", err)
		return
	}

	macStr := mac.String()

	if withdraw && vtep == "" {
		if stored, ok := o.macVTEP.Load(macStr); ok {
			if storedVTEP, ok := stored.(string); ok {
				vtep = storedVTEP
			}
		} else {
			o.log.Debug("type-2 withdraw with no tracked VTEP", "mac", macStr)
			return
		}
	}

	remoteIP := net.ParseIP(vtep)
	if remoteIP == nil {
		o.log.Debug("type-2 route with no valid next-hop", "vtep", vtep)
		return
	}

	if vtep == o.cfg.RouterID {
		return
	}

	vxlanName := o.vxlanName()
	vxLink, err := o.fdb.LinkByName(vxlanName)
	if err != nil {
		o.log.Warn("cannot find VXLAN for FDB update", "vxlan", vxlanName, "error", err)
		return
	}

	fdb := &netlink.Neigh{
		LinkIndex:    vxLink.Attrs().Index,
		Family:       syscall.AF_BRIDGE,
		HardwareAddr: mac,
		IP:           remoteIP,
		Flags:        netlink.NTF_SELF,
		State:        netlink.NUD_PERMANENT,
	}

	if withdraw {
		if err := o.fdb.NeighDel(fdb); err != nil {
			o.log.Debug("failed to delete FDB entry", "mac", mac, "vtep", vtep, "error", err)
		} else {
			o.log.Info("removed FDB entry from type-2 withdraw", "mac", mac, "vtep", vtep)
		}
		o.macVTEP.Delete(macStr)
		return
	}

	if err := o.fdb.NeighSet(fdb); err != nil {
		o.log.Debug("failed to add/update FDB entry", "mac", mac, "vtep", vtep, "error", err)
	} else {
		o.log.Info("installed FDB entry from type-2 route", "mac", mac, "vtep", vtep)
	}
	o.macVTEP.Store(macStr, vtep)
}

// handleType3Route installs or removes a BUM FDB entry for a remote VTEP
// learned via EVPN Type-3 (Inclusive Multicast Ethernet Tag) route.
// Only active when EnableL2 is set.
func (o *OverlayTier) handleType3Route(route *apipb.EVPNInclusiveMulticastEthernetTagRoute, vtep string, withdraw bool) {
	remoteIP := net.ParseIP(route.GetIpAddress())
	if remoteIP == nil {
		remoteIP = net.ParseIP(vtep)
	}
	if remoteIP == nil {
		o.log.Debug("type-3 route with no valid VTEP IP")
		return
	}

	if remoteIP.String() == o.cfg.RouterID {
		return
	}

	vxlanName := o.vxlanName()
	vxLink, err := o.fdb.LinkByName(vxlanName)
	if err != nil {
		o.log.Warn("cannot find VXLAN for BUM update", "vxlan", vxlanName, "error", err)
		return
	}

	fdb := &netlink.Neigh{
		LinkIndex:    vxLink.Attrs().Index,
		Family:       syscall.AF_BRIDGE,
		HardwareAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0},
		IP:           remoteIP,
		Flags:        netlink.NTF_SELF,
		State:        netlink.NUD_PERMANENT,
	}

	if withdraw {
		if err := o.fdb.NeighDel(fdb); err != nil {
			o.log.Debug("failed to delete BUM FDB entry", "vtep", remoteIP, "error", err)
		} else {
			o.log.Info("removed BUM FDB entry from type-3 withdraw", "vtep", remoteIP)
		}
		return
	}

	if err := o.fdb.NeighAppend(fdb); err != nil {
		o.log.Debug("failed to append BUM FDB entry", "vtep", remoteIP, "error", err)
	} else {
		o.log.Info("installed BUM FDB entry from type-3 route", "vtep", remoteIP)
	}
}

// parsePrefixRoute parses a prefix string and length into a *net.IPNet.
func parsePrefixRoute(prefix string, prefixLen uint32) (*net.IPNet, error) {
	ip := net.ParseIP(prefix)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP %q", prefix)
	}

	var mask net.IPMask
	if ip.To4() != nil {
		if prefixLen > 32 {
			return nil, fmt.Errorf("invalid IPv4 prefix length %d", prefixLen)
		}
		mask = net.CIDRMask(int(prefixLen), 32)
	} else {
		if prefixLen > 128 {
			return nil, fmt.Errorf("invalid IPv6 prefix length %d", prefixLen)
		}
		mask = net.CIDRMask(int(prefixLen), 128)
	}

	return &net.IPNet{IP: ip.Mask(mask), Mask: mask}, nil
}

// extractNextHop returns the first next-hop IP from a path's MpReachNLRI
// attribute. Returns empty string if not found.
func extractNextHop(p *apipb.Path) string {
	for _, attr := range p.GetPattrs() {
		msg, err := attr.UnmarshalNew()
		if err != nil {
			continue
		}
		if mpReach, ok := msg.(*apipb.MpReachNLRIAttribute); ok {
			if hops := mpReach.GetNextHops(); len(hops) > 0 {
				return hops[0]
			}
		}
	}
	return ""
}

// buildRouteDistinguisher builds an RD, selecting 2-octet or 4-octet ASN type.
func buildRouteDistinguisher(asn, vni uint32) (*anypb.Any, error) {
	var a *anypb.Any
	var err error
	if asn <= asnMax2Byte {
		a, err = anypb.New(&apipb.RouteDistinguisherTwoOctetASN{
			Admin:    asn,
			Assigned: vni,
		})
	} else {
		a, err = anypb.New(&apipb.RouteDistinguisherFourOctetASN{
			Admin:    asn,
			Assigned: vni & 0xFFFF,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("marshal route distinguisher: %w", err)
	}
	return a, nil
}

// buildEVPNType5NLRI builds an EVPN IP Prefix (Type-5) NLRI for the given
// IP prefix (typically a /32 host route), so the fabric can route overlay
// traffic to this VTEP.
func buildEVPNType5NLRI(rd *anypb.Any, provisionIP, gwIP string, label uint32) (*anypb.Any, error) {
	_, ipNet, err := net.ParseCIDR(provisionIP)
	if err != nil {
		return nil, fmt.Errorf("parse provision IP %s: %w", provisionIP, err)
	}

	ones, _ := ipNet.Mask.Size()

	route := &apipb.EVPNIPPrefixRoute{
		Rd: rd,
		Esi: &apipb.EthernetSegmentIdentifier{
			Type:  0,
			Value: make([]byte, 9),
		},
		EthernetTag: 0,
		IpPrefixLen: uint32(ones),
		IpPrefix:    ipNet.IP.String(),
		GwAddress:   gwIP,
		Label:       label,
	}
	a, err := anypb.New(route)
	if err != nil {
		return nil, fmt.Errorf("marshal EVPN type-5 NLRI: %w", err)
	}
	return a, nil
}

// buildType5PathAttrs builds BGP path attributes for EVPN Type-5 advertisement.
func buildType5PathAttrs(nlri *anypb.Any, nextHop string, asn, vni uint32) ([]*anypb.Any, error) {
	origin, err := anypb.New(&apipb.OriginAttribute{Origin: 0}) // IGP
	if err != nil {
		return nil, fmt.Errorf("marshal origin: %w", err)
	}

	// GoBGP requires MpReachNLRIAttribute to carry both the next-hop and the
	// NLRI list for EVPN address families.
	mpReach, err := anypb.New(&apipb.MpReachNLRIAttribute{
		Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		NextHops: []string{nextHop},
		Nlris:    []*anypb.Any{nlri},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mp-reach: %w", err)
	}

	rt, err := buildRouteTarget(asn, vni)
	if err != nil {
		return nil, fmt.Errorf("build route target: %w", err)
	}

	extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
		Communities: []*anypb.Any{rt},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ext-communities: %w", err)
	}

	return []*anypb.Any{origin, mpReach, extComm}, nil
}

// matchesLocalRT reports whether path carries an extended community Route Target
// matching the given localASN and localVNI. It iterates path attributes looking
// for an ExtendedCommunitiesAttribute and checks each community entry against
// the expected RT value (same logic as buildRouteTarget).
func matchesLocalRT(path *apipb.Path, localASN, localVNI uint32) bool {
	for _, attr := range path.GetPattrs() {
		msg, err := attr.UnmarshalNew()
		if err != nil {
			continue
		}
		extComm, ok := msg.(*apipb.ExtendedCommunitiesAttribute)
		if !ok {
			continue
		}
		if rtFoundInCommunities(extComm.GetCommunities(), localASN, localVNI) {
			return true
		}
	}
	return false
}

// rtFoundInCommunities checks a slice of extended community Any values for a
// Route Target matching localASN and localVNI.
func rtFoundInCommunities(communities []*anypb.Any, localASN, localVNI uint32) bool {
	for _, c := range communities {
		msg, err := c.UnmarshalNew()
		if err != nil {
			continue
		}
		if rtCommunityMatches(msg, localASN, localVNI) {
			return true
		}
	}
	return false
}

// rtCommunityMatches returns true if the proto message represents a Route
// Target extended community (SubType 0x02) matching the given ASN and VNI.
// For 4-octet ASN the VNI is masked to 16 bits, mirroring buildRouteTarget.
func rtCommunityMatches(msg interface{}, localASN, localVNI uint32) bool {
	const rtSubType = uint32(0x02)
	switch v := msg.(type) {
	case *apipb.TwoOctetAsSpecificExtended:
		return v.GetSubType() == rtSubType &&
			v.GetAsn() == localASN &&
			v.GetLocalAdmin() == localVNI
	case *apipb.FourOctetAsSpecificExtended:
		return v.GetSubType() == rtSubType &&
			v.GetAsn() == localASN &&
			v.GetLocalAdmin() == localVNI&0xFFFF
	}
	return false
}

// buildRouteTarget builds a route target extended community (Type 0x02),
// selecting 2-octet or 4-octet format based on ASN size.
func buildRouteTarget(asn, vni uint32) (*anypb.Any, error) {
	var a *anypb.Any
	var err error
	if asn <= asnMax2Byte {
		a, err = anypb.New(&apipb.TwoOctetAsSpecificExtended{
			IsTransitive: true,
			SubType:      0x02, // Route Target
			Asn:          asn,
			LocalAdmin:   vni,
		})
	} else {
		a, err = anypb.New(&apipb.FourOctetAsSpecificExtended{
			IsTransitive: true,
			SubType:      0x02, // Route Target
			Asn:          asn,
			LocalAdmin:   vni & 0xFFFF,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("marshal route target: %w", err)
	}
	return a, nil
}

// pmsiTunnelTypeIngressReplication is the PMSI tunnel type for ingress
// replication (RFC 6514 §5). Remote VTEPs replicate BUM frames to this VTEP.
const pmsiTunnelTypeIngressReplication = 6

// buildEVPNType3NLRI builds an EVPN Inclusive Multicast Ethernet Tag (Type-3)
// NLRI. The IMET route tells the fabric to send BUM traffic to this VTEP.
func buildEVPNType3NLRI(rd *anypb.Any, routerID string) (*anypb.Any, error) {
	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{
		Rd:          rd,
		EthernetTag: 0,
		IpAddress:   routerID,
	}
	a, err := anypb.New(route)
	if err != nil {
		return nil, fmt.Errorf("marshal EVPN type-3 NLRI: %w", err)
	}
	return a, nil
}

// buildType3PathAttrs builds BGP path attributes for EVPN Type-3 (IMET)
// advertisement, including Origin, MpReach, Route Target, and PMSI Tunnel.
func buildType3PathAttrs(nlri *anypb.Any, nextHop string, asn, vni uint32) ([]*anypb.Any, error) {
	origin, err := anypb.New(&apipb.OriginAttribute{Origin: 0})
	if err != nil {
		return nil, fmt.Errorf("marshal origin: %w", err)
	}

	mpReach, err := anypb.New(&apipb.MpReachNLRIAttribute{
		Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		NextHops: []string{nextHop},
		Nlris:    []*anypb.Any{nlri},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mp-reach: %w", err)
	}

	rt, err := buildRouteTarget(asn, vni)
	if err != nil {
		return nil, fmt.Errorf("build route target: %w", err)
	}

	extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
		Communities: []*anypb.Any{rt},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ext-communities: %w", err)
	}

	pmsi, err := anypb.New(&apipb.PmsiTunnelAttribute{
		Flags: 0,
		Type:  pmsiTunnelTypeIngressReplication,
		Label: vni,
		Id:    net.ParseIP(nextHop).To4(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal pmsi tunnel: %w", err)
	}

	return []*anypb.Any{origin, mpReach, extComm, pmsi}, nil
}

// buildEVPNType2NLRI builds an EVPN MAC/IP Advertisement (Type-2) NLRI for
// the given MAC address and optional IP. Used to announce the local bridge
// MAC so remote VTEPs learn the FDB entry via control-plane.
func buildEVPNType2NLRI(rd *anypb.Any, mac, ip string, label uint32) (*anypb.Any, error) {
	route := &apipb.EVPNMACIPAdvertisementRoute{
		Rd: rd,
		Esi: &apipb.EthernetSegmentIdentifier{
			Type:  0,
			Value: make([]byte, 9),
		},
		EthernetTag: 0,
		MacAddress:  mac,
		IpAddress:   ip,
		Labels:      []uint32{label},
	}
	a, err := anypb.New(route)
	if err != nil {
		return nil, fmt.Errorf("marshal EVPN type-2 NLRI: %w", err)
	}
	return a, nil
}

// buildType2PathAttrs builds BGP path attributes for EVPN Type-2 (MAC/IP)
// advertisement, including Origin, MpReach, and Route Target.
func buildType2PathAttrs(nlri *anypb.Any, nextHop string, asn, vni uint32) ([]*anypb.Any, error) {
	origin, err := anypb.New(&apipb.OriginAttribute{Origin: 0})
	if err != nil {
		return nil, fmt.Errorf("marshal origin: %w", err)
	}

	mpReach, err := anypb.New(&apipb.MpReachNLRIAttribute{
		Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		NextHops: []string{nextHop},
		Nlris:    []*anypb.Any{nlri},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mp-reach: %w", err)
	}

	rt, err := buildRouteTarget(asn, vni)
	if err != nil {
		return nil, fmt.Errorf("build route target: %w", err)
	}

	extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
		Communities: []*anypb.Any{rt},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ext-communities: %w", err)
	}

	return []*anypb.Any{origin, mpReach, extComm}, nil
}
