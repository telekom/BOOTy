//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
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
	NeighDel(neigh *netlink.Neigh) error
}

// netlinkFDB is the production implementation using real netlink calls.
type netlinkFDB struct{}

func (netlinkFDB) LinkByName(name string) (netlink.Link, error) { return netlink.LinkByName(name) }
func (netlinkFDB) NeighSet(n *netlink.Neigh) error              { return netlink.NeighSet(n) }
func (netlinkFDB) NeighDel(n *netlink.Neigh) error              { return netlink.NeighDel(n) }

// OverlayTier manages EVPN Type-5 routes and VXLAN encapsulation.
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

	// macVTEP tracks MAC → VTEP mappings learned from Type-2 routes
	// so that withdrawals (which lack next-hop) can still delete the
	// correct FDB entry.
	macVTEP map[string]string
}

// NewOverlayTier creates a new overlay tier.
func NewOverlayTier(cfg *Config) *OverlayTier {
	return &OverlayTier{
		cfg:     cfg,
		log:     slog.With("tier", "overlay"),
		macVTEP: make(map[string]string),
		fdb:     netlinkFDB{},
	}
}

// SetBgpServer sets the shared BGP server from the underlay tier.
func (o *OverlayTier) SetBgpServer(s *server.BgpServer) {
	o.bgp = s
}

// Setup creates VXLAN, bridge, and advertises EVPN Type-5 routes.
// VRF creation is handled by the stack before underlay/overlay setup.
func (o *OverlayTier) Setup(ctx context.Context) error {
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
			return fmt.Errorf("add gateway FDB entry: %w", err)
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
	if err := netlink.NeighSet(fdb); err != nil {
		return fmt.Errorf("set BUM FDB entry for %s: %w", o.cfg.ProvisionGateway, err)
	}

	o.log.Info("added gateway BUM FDB entry", "vxlan", vxLink.Attrs().Name, "vtep", o.cfg.ProvisionGateway)
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

func (o *OverlayTier) advertiseType5(ctx context.Context) error {
	rd, err := buildRouteDistinguisher(o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
	if err != nil {
		return fmt.Errorf("build route distinguisher: %w", err)
	}

	nlri, err := buildEVPNType5NLRI(rd, o.cfg.RouterID, uint32(o.cfg.ProvisionVNI))
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

	o.log.Info("advertised EVPN type-5 default route", "vni", o.cfg.ProvisionVNI)
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
// appropriate handler based on NLRI type.
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

	msg, err := nlri.UnmarshalNew()
	if err != nil {
		o.log.Debug("route update unmarshal failed", "error", err)
		return
	}

	vtep := extractNextHop(p)

	switch route := msg.(type) {
	case *apipb.EVPNMACIPAdvertisementRoute:
		o.handleType2Route(route, vtep, withdraw)
	case *apipb.EVPNInclusiveMulticastEthernetTagRoute:
		o.handleType3Route(route, vtep, withdraw)
	default:
		o.log.Debug("route update", "action", action, "type", nlri.GetTypeUrl())
	}
}

// handleType2Route installs or removes an FDB entry for a remote MAC learned
// via EVPN Type-2 (MAC/IP Advertisement) route. This enables unicast VXLAN
// forwarding to remote VTEPs without data-plane MAC learning.
func (o *OverlayTier) handleType2Route(route *apipb.EVPNMACIPAdvertisementRoute, vtep string, withdraw bool) {
	mac, err := net.ParseMAC(route.GetMacAddress())
	if err != nil {
		o.log.Debug("type-2 route with invalid MAC", "mac", route.GetMacAddress(), "error", err)
		return
	}

	macStr := mac.String()

	// For withdrawals, BGP uses MP_UNREACH_NLRI which has no next-hop.
	// Look up the previously stored VTEP for this MAC.
	if withdraw && vtep == "" {
		if stored, ok := o.macVTEP[macStr]; ok {
			vtep = stored
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

	// Skip FDB entries for our own VTEP.
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
		delete(o.macVTEP, macStr)
		return
	}

	if err := o.fdb.NeighSet(fdb); err != nil {
		o.log.Debug("failed to add/update FDB entry", "mac", mac, "vtep", vtep, "error", err)
	} else {
		o.log.Info("installed FDB entry from type-2 route", "mac", mac, "vtep", vtep)
	}
	o.macVTEP[macStr] = vtep
}

// handleType3Route installs or removes a BUM FDB entry for a remote VTEP
// learned via EVPN Type-3 (Inclusive Multicast Ethernet Tag) route.
// This ensures broadcast/unknown/multicast traffic is flooded to all
// participating VTEPs in the VNI.
func (o *OverlayTier) handleType3Route(route *apipb.EVPNInclusiveMulticastEthernetTagRoute, vtep string, withdraw bool) {
	remoteIP := net.ParseIP(route.GetIpAddress())
	if remoteIP == nil {
		remoteIP = net.ParseIP(vtep)
	}
	if remoteIP == nil {
		o.log.Debug("type-3 route with no valid VTEP IP")
		return
	}

	// Skip BUM entries for our own VTEP.
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

	if err := o.fdb.NeighSet(fdb); err != nil {
		o.log.Debug("failed to add/update BUM FDB entry", "vtep", remoteIP, "error", err)
	} else {
		o.log.Info("installed BUM FDB entry from type-3 route", "vtep", remoteIP)
	}
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

// buildEVPNType5NLRI builds an EVPN IP Prefix (Type-5) NLRI for default route.
func buildEVPNType5NLRI(rd *anypb.Any, gwAddr string, label uint32) (*anypb.Any, error) {
	route := &apipb.EVPNIPPrefixRoute{
		Rd: rd,
		Esi: &apipb.EthernetSegmentIdentifier{
			Type:  0,
			Value: make([]byte, 9),
		},
		EthernetTag: 0,
		IpPrefix:    "0.0.0.0",
		IpPrefixLen: 0,
		GwAddress:   gwAddr,
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
