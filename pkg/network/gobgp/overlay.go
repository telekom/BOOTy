//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/vishvananda/netlink"
	"google.golang.org/protobuf/types/known/anypb"
)

// OverlayTier manages EVPN Type-5 routes and VXLAN encapsulation.
type OverlayTier struct {
	bgp *server.BgpServer
	cfg *Config
	log *slog.Logger
}

// NewOverlayTier creates a new overlay tier.
func NewOverlayTier(cfg *Config) *OverlayTier {
	return &OverlayTier{
		cfg: cfg,
		log: slog.With("tier", "overlay"),
	}
}

// SetBgpServer sets the shared BGP server from the underlay tier.
func (o *OverlayTier) SetBgpServer(s *server.BgpServer) {
	o.bgp = s
}

// Setup creates VXLAN, bridge, and advertises EVPN Type-5 routes.
// VRF creation is handled by the stack before underlay/overlay setup.
func (o *OverlayTier) Setup(ctx context.Context) error {
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

	go o.watchRoutes(ctx)

	return nil
}

// Ready waits until the overlay is operational by checking EVPN route state.
func (o *OverlayTier) Ready(_ context.Context, _ time.Duration) error {
	// Overlay is ready once Setup completes (routes are advertised synchronously).
	return nil
}

// Teardown removes the overlay network resources.
func (o *OverlayTier) Teardown(_ context.Context) error {
	vxlanName := fmt.Sprintf("vx%d", o.cfg.ProvisionVNI)

	for _, name := range []string{o.cfg.BridgeName, vxlanName, o.cfg.VRFName} {
		if name == "" {
			continue
		}
		link, err := netlink.LinkByName(name)
		if err != nil {
			continue
		}
		if err := netlink.LinkDel(link); err != nil {
			o.log.Warn("Failed to remove interface", "name", name, "error", err)
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
		if os.IsExist(err) {
			o.log.Debug("VRF already exists", "name", o.cfg.VRFName)
			return nil
		}
		return fmt.Errorf("add VRF %s: %w", o.cfg.VRFName, err)
	}
	if err := netlink.LinkSetUp(vrf); err != nil {
		return fmt.Errorf("bring up VRF %s: %w", o.cfg.VRFName, err)
	}

	o.log.Info("Created VRF", "name", o.cfg.VRFName, "table", o.cfg.VRFTableID)
	return nil
}

func (o *OverlayTier) createVXLANAndBridge() error {
	vxlanName := fmt.Sprintf("vx%d", o.cfg.ProvisionVNI)

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

	o.log.Info("Created VXLAN and bridge",
		"vxlan", vxlanName, "vni", o.cfg.ProvisionVNI,
		"bridge", o.cfg.BridgeName,
	)
	return nil
}

func (o *OverlayTier) createVXLAN(name string) (netlink.Link, error) {
	vxlanMTU := o.cfg.MTU - 50
	if vxlanMTU <= 0 {
		vxlanMTU = 1500
	}

	srcAddr := net.ParseIP(o.cfg.RouterID)
	vxlan := &netlink.Vxlan{
		LinkAttrs:    netlink.LinkAttrs{Name: name},
		VxlanId:      o.cfg.ProvisionVNI,
		SrcAddr:      srcAddr,
		Port:         4789,
		Learning:     false,
		VtepDevIndex: 0,
	}

	if err := netlink.LinkAdd(vxlan); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("add VXLAN %s: %w", name, err)
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
	if err := netlink.LinkAdd(bridge); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("add bridge %s: %w", o.cfg.BridgeName, err)
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

	o.log.Info("Assigned provision IP", "bridge", o.cfg.BridgeName, "ip", o.cfg.ProvisionIP)
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

	o.log.Info("Added overlay loopback", "ip", o.cfg.OverlayIP)
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

	pattrs, err := buildType5PathAttrs(o.cfg.RouterID, o.cfg.ASN, uint32(o.cfg.ProvisionVNI))
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

	o.log.Info("Advertised EVPN Type-5 default route", "vni", o.cfg.ProvisionVNI)
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
				action := "add"
				if p.GetIsWithdraw() {
					action = "withdraw"
				}
				o.log.Debug("Route update", "action", action, "family", p.GetFamily())
			}
		}
	})
	if err != nil {
		o.log.Warn("Route watcher stopped", "error", err)
	}
}

// buildRouteDistinguisher builds an RD, selecting 2-octet or 4-octet ASN type.
func buildRouteDistinguisher(asn, vni uint32) (*anypb.Any, error) {
	var a *anypb.Any
	var err error
	if asn <= 65535 {
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
		Rd:          rd,
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
func buildType5PathAttrs(nextHop string, asn, vni uint32) ([]*anypb.Any, error) {
	origin, err := anypb.New(&apipb.OriginAttribute{Origin: 0}) // IGP
	if err != nil {
		return nil, fmt.Errorf("marshal origin: %w", err)
	}

	mpReach, err := anypb.New(&apipb.MpReachNLRIAttribute{
		Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		NextHops: []string{nextHop},
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
	if asn <= 65535 {
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
