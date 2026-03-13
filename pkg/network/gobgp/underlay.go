//go:build linux

package gobgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"syscall"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/vishvananda/netlink"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/telekom/BOOTy/pkg/network"
)

// UnderlayTier manages BGP peering for VXLAN reachability.
// Depending on PeerMode it establishes unnumbered (link-local),
// numbered (explicit IP), or a combination of both session types.
type UnderlayTier struct {
	bgp  *server.BgpServer
	cfg  *Config
	nics []string
	log  *slog.Logger
}

// NewUnderlayTier creates a new underlay tier.
func NewUnderlayTier(cfg *Config) *UnderlayTier {
	return &UnderlayTier{
		cfg: cfg,
		log: slog.With("tier", "underlay"),
	}
}

// BgpServer returns the shared BGP server for the overlay tier.
func (u *UnderlayTier) BgpServer() *server.BgpServer {
	return u.bgp
}

// NICs returns the detected physical NICs.
func (u *UnderlayTier) NICs() []string {
	return u.nics
}

// Setup creates the underlay loopback, detects NICs (when needed), starts the
// BGP server, and adds peers according to the configured PeerMode.
func (u *UnderlayTier) Setup(ctx context.Context) error {
	if err := u.createUnderlayDummy(); err != nil {
		return fmt.Errorf("create underlay dummy: %w", err)
	}

	// Unnumbered and dual modes need physical NICs for interface-based peering.
	if u.cfg.PeerMode != network.PeerModeNumbered {
		nics, err := network.DetectPhysicalNICs()
		if err != nil {
			return fmt.Errorf("detect NICs: %w", err)
		}
		if len(nics) == 0 {
			return fmt.Errorf("no physical NICs detected")
		}
		u.nics = nics
		u.log.Info("Detected physical NICs", "nics", nics)

		if err := u.configureNICs(); err != nil {
			return fmt.Errorf("configure NICs: %w", err)
		}
	}

	if err := enableForwarding(u.log); err != nil {
		return fmt.Errorf("enable forwarding: %w", err)
	}

	if err := u.startBgpServer(ctx); err != nil {
		return fmt.Errorf("start BGP server: %w", err)
	}

	if err := u.addPeers(ctx); err != nil {
		return fmt.Errorf("add BGP peers: %w", err)
	}

	if err := u.announceUnderlayRoute(ctx); err != nil {
		return fmt.Errorf("announce underlay route: %w", err)
	}

	return nil
}

// Ready waits until at least one BGP peer reaches ESTABLISHED state.
func (u *UnderlayTier) Ready(ctx context.Context, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-timer.C:
			return fmt.Errorf("timed out waiting for BGP peers to establish")
		case <-ticker.C:
			if u.hasEstablishedPeer(ctx) {
				u.log.Info("Underlay BGP peer established")
				return nil
			}
		}
	}
}

// Teardown stops the BGP server and removes the underlay dummy interface.
func (u *UnderlayTier) Teardown(_ context.Context) error {
	if u.bgp != nil {
		u.bgp.Stop()
		u.log.Info("BGP server stopped")
	}

	link, err := netlink.LinkByName("dummy.underlay")
	if err == nil {
		if delErr := netlink.LinkDel(link); delErr != nil {
			u.log.Warn("Failed to remove underlay dummy", "error", delErr)
		}
	}

	return nil
}

func (u *UnderlayTier) createUnderlayDummy() error {
	dummy := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "dummy.underlay"},
	}
	if err := netlink.LinkAdd(dummy); err != nil && !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("add dummy.underlay: %w", err)
	}

	link, err := netlink.LinkByName("dummy.underlay")
	if err != nil {
		return fmt.Errorf("find dummy.underlay: %w", err)
	}

	addr, err := netlink.ParseAddr(u.cfg.RouterID + "/32")
	if err != nil {
		return fmt.Errorf("parse router ID %s: %w", u.cfg.RouterID, err)
	}

	if err := netlink.AddrAdd(link, addr); err != nil && !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("add addr to dummy.underlay: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up dummy.underlay: %w", err)
	}

	// Assign dummy to VRF for traffic isolation.
	if u.cfg.VRFName != "" {
		vrfLink, err := netlink.LinkByName(u.cfg.VRFName)
		if err != nil {
			return fmt.Errorf("find VRF %s for dummy assignment: %w", u.cfg.VRFName, err)
		}
		if err := netlink.LinkSetMasterByIndex(link, vrfLink.Attrs().Index); err != nil {
			return fmt.Errorf("assign dummy to VRF %s: %w", u.cfg.VRFName, err)
		}
	}

	u.log.Info("Created underlay dummy", "ip", u.cfg.RouterID)
	return nil
}

func (u *UnderlayTier) configureNICs() error {
	for _, nic := range u.nics {
		link, err := netlink.LinkByName(nic)
		if err != nil {
			u.log.Warn("Failed to find NIC", "nic", nic, "error", err)
			continue
		}

		if err := netlink.LinkSetMTU(link, u.cfg.MTU); err != nil {
			u.log.Warn("Failed to set MTU", "nic", nic, "error", err)
		}

		if err := netlink.LinkSetUp(link); err != nil {
			u.log.Warn("Failed to bring up NIC", "nic", nic, "error", err)
		}

		// Assign NIC to VRF for traffic isolation.
		if u.cfg.VRFName != "" {
			vrfLink, err := netlink.LinkByName(u.cfg.VRFName)
			if err != nil {
				u.log.Warn("Failed to find VRF for NIC assignment", "nic", nic, "vrf", u.cfg.VRFName, "error", err)
			} else if err := netlink.LinkSetMasterByIndex(link, vrfLink.Attrs().Index); err != nil {
				u.log.Warn("Failed to assign NIC to VRF", "nic", nic, "error", err)
			}
		}
	}
	return nil
}

func (u *UnderlayTier) startBgpServer(ctx context.Context) error {
	u.bgp = server.NewBgpServer()
	go u.bgp.Serve()

	if err := u.bgp.StartBgp(ctx, &apipb.StartBgpRequest{
		Global: &apipb.Global{
			Asn:        u.cfg.ASN,
			RouterId:   u.cfg.RouterID,
			ListenPort: u.cfg.ListenPort,
		},
	}); err != nil {
		u.bgp.Stop()
		u.bgp = nil
		return fmt.Errorf("start BGP: %w", err)
	}

	u.log.Info("BGP server started",
		"asn", u.cfg.ASN,
		"routerID", u.cfg.RouterID,
		"port", u.cfg.ListenPort,
	)
	return nil
}

func (u *UnderlayTier) addPeers(ctx context.Context) error {
	var added int
	var lastErr error

	switch u.cfg.PeerMode {
	case network.PeerModeUnnumbered:
		added, lastErr = u.addInterfacePeers(ctx, allFamilies())
	case network.PeerModeDual:
		added, lastErr = u.addInterfacePeers(ctx, ipv4Families())
		n, err := u.addNumberedPeers(ctx, allFamilies())
		added += n
		if err != nil {
			lastErr = err
		}
	case network.PeerModeNumbered:
		added, lastErr = u.addNumberedPeers(ctx, allFamilies())
	}

	if added == 0 && lastErr != nil {
		return fmt.Errorf("no BGP peers added: %w", lastErr)
	}
	return nil
}

func (u *UnderlayTier) addInterfacePeers(ctx context.Context, families []*apipb.AfiSafi) (int, error) {
	var added int
	var lastErr error
	for _, nic := range u.nics {
		if err := u.addInterfacePeer(ctx, nic, families); err != nil {
			u.log.Warn("Failed to add unnumbered peer", "nic", nic, "error", err)
			lastErr = err
		} else {
			added++
		}
	}
	return added, lastErr
}

func (u *UnderlayTier) addNumberedPeers(ctx context.Context, families []*apipb.AfiSafi) (int, error) {
	var added int
	var lastErr error
	for _, addr := range u.cfg.NeighborAddrs {
		if err := u.addNumberedPeer(ctx, addr, families); err != nil {
			u.log.Warn("Failed to add numbered peer", "addr", addr, "error", err)
			lastErr = err
		} else {
			added++
		}
	}
	return added, lastErr
}

func (u *UnderlayTier) addInterfacePeer(ctx context.Context, iface string, families []*apipb.AfiSafi) error {
	// GoBGP's AddPeer has a bug where ExtractNeighborAddress is called before
	// SetDefaultNeighborConfigValues, so NeighborInterface is never resolved.
	// Work around by resolving the link-local address ourselves.
	addr, err := discoverLinkLocalPeer(iface)
	if err != nil {
		return fmt.Errorf("discover link-local peer on %s: %w", iface, err)
	}

	peer := &apipb.Peer{
		Conf: &apipb.PeerConf{
			NeighborAddress:   addr,
			NeighborInterface: iface,
			PeerAsn:           0, // External peer, ASN learned via open
		},
		Timers:   bgpTimers(u.cfg),
		AfiSafis: families,
		Transport: &apipb.Transport{
			MtuDiscovery: true,
		},
	}

	if err := u.bgp.AddPeer(ctx, &apipb.AddPeerRequest{Peer: peer}); err != nil {
		return fmt.Errorf("add peer on %s: %w", iface, err)
	}

	u.log.Info("Added unnumbered BGP peer", "interface", iface, "address", addr)
	return nil
}

func (u *UnderlayTier) addNumberedPeer(ctx context.Context, addr string, families []*apipb.AfiSafi) error {
	remoteASN := u.cfg.RemoteASN
	if remoteASN == 0 {
		remoteASN = u.cfg.ASN // iBGP
	}

	peer := &apipb.Peer{
		Conf: &apipb.PeerConf{
			NeighborAddress: addr,
			PeerAsn:         remoteASN,
		},
		Timers:   bgpTimers(u.cfg),
		AfiSafis: families,
		Transport: &apipb.Transport{
			MtuDiscovery: true,
		},
	}

	sessionType := "iBGP"
	if remoteASN != u.cfg.ASN {
		sessionType = "eBGP"
	}

	if err := u.bgp.AddPeer(ctx, &apipb.AddPeerRequest{Peer: peer}); err != nil {
		return fmt.Errorf("add %s peer %s: %w", sessionType, addr, err)
	}

	u.log.Info("Added numbered BGP peer", "address", addr, "type", sessionType, "remoteASN", remoteASN)
	return nil
}

func (u *UnderlayTier) hasEstablishedPeer(ctx context.Context) bool {
	established := false

	fn := func(p *apipb.Peer) {
		if p.GetState().GetSessionState() == apipb.PeerState_ESTABLISHED {
			established = true
		}
	}

	err := u.bgp.ListPeer(ctx, &apipb.ListPeerRequest{}, fn)
	if err != nil {
		u.log.Debug("Failed to list peers", "error", err)
		return false
	}

	return established
}

// announceUnderlayRoute advertises the RouterID /32 via IPv4 unicast so that
// remote peers (e.g. FRR spine) learn the underlay loopback as a BGP route.
func (u *UnderlayTier) announceUnderlayRoute(ctx context.Context) error {
	ip := net.ParseIP(u.cfg.RouterID).To4()
	if ip == nil {
		return fmt.Errorf("invalid router ID %q", u.cfg.RouterID)
	}

	nlri, err := anypb.New(&apipb.IPAddressPrefix{
		PrefixLen: 32,
		Prefix:    u.cfg.RouterID,
	})
	if err != nil {
		return fmt.Errorf("build NLRI: %w", err)
	}

	origin, err := anypb.New(&apipb.OriginAttribute{Origin: 0}) // IGP
	if err != nil {
		return fmt.Errorf("build origin attr: %w", err)
	}

	nexthop, err := anypb.New(&apipb.NextHopAttribute{NextHop: u.cfg.RouterID})
	if err != nil {
		return fmt.Errorf("build next-hop attr: %w", err)
	}

	aspath, err := anypb.New(&apipb.AsPathAttribute{
		Segments: []*apipb.AsSegment{},
	})
	if err != nil {
		return fmt.Errorf("build as-path attr: %w", err)
	}

	_, err = u.bgp.AddPath(ctx, &apipb.AddPathRequest{
		Path: &apipb.Path{
			Family: &apipb.Family{Afi: apipb.Family_AFI_IP, Safi: apipb.Family_SAFI_UNICAST},
			Nlri:   nlri,
			Pattrs: []*anypb.Any{origin, nexthop, aspath},
		},
	})
	if err != nil {
		return fmt.Errorf("add underlay route: %w", err)
	}

	u.log.Info("Announced underlay route", "prefix", u.cfg.RouterID+"/32")
	return nil
}

// discoverLinkLocalPeer finds the remote peer's link-local IPv6 address on the
// given interface by polling the NDP neighbor table. An ICMPv6 ping to the
// all-nodes multicast group is sent first to trigger neighbor discovery.
func discoverLinkLocalPeer(iface string) (string, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return "", fmt.Errorf("interface %s: %w", iface, err)
	}

	// Trigger NDP by pinging the all-nodes multicast address.
	go triggerNDP(iface) //nolint:errcheck // best-effort NDP solicitation

	for range 20 {
		addr, found := findLinkLocalNeighbor(ifi, iface)
		if found {
			return addr, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return "", fmt.Errorf("no IPv6 link-local neighbor found on %s after 10s", iface)
}

// triggerNDP sends an ICMPv6 echo to ff02::1 (all-nodes) on the interface to
// populate the NDP neighbor table.
func triggerNDP(iface string) {
	// Try raw socket first (requires CAP_NET_RAW).
	lc := net.ListenConfig{}
	conn, err := lc.ListenPacket(context.Background(), "ip6:ipv6-icmp", "::"+"%"+iface)
	if err != nil {
		// Fallback: use the ip command to trigger NDP via neighbor solicitation.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "ip", "-6", "neigh", "show", "dev", iface).Run() //nolint:gosec // constant args
		return
	}
	defer conn.Close() //nolint:errcheck // best-effort NDP solicitation

	dst := &net.UDPAddr{IP: net.ParseIP("ff02::1"), Zone: iface}
	// ICMPv6 echo request: type=128, code=0, checksum=0 (kernel fills), id=0, seq=1
	msg := []byte{128, 0, 0, 0, 0, 0, 0, 1}
	_, _ = conn.WriteTo(msg, dst)
}

// findLinkLocalNeighbor looks for exactly one non-local link-local IPv6
// neighbor on the given interface.
func findLinkLocalNeighbor(ifi *net.Interface, iface string) (string, bool) {
	neighs, err := netlink.NeighList(ifi.Index, netlink.FAMILY_V6)
	if err != nil {
		return "", false
	}

	for i := range neighs {
		n := &neighs[i]
		if n.State&netlink.NUD_FAILED != 0 {
			continue
		}
		if !n.IP.IsLinkLocalUnicast() {
			continue
		}
		if isOwnAddress(ifi, n.IP) {
			continue
		}
		return fmt.Sprintf("%s%%%s", n.IP, iface), true
	}
	return "", false
}

// isOwnAddress checks if the given IP belongs to the interface.
func isOwnAddress(ifi *net.Interface, ip net.IP) bool {
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if parsed, _, _ := net.ParseCIDR(a.String()); ip.Equal(parsed) {
			return true
		}
	}
	return false
}
