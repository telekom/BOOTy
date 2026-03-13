//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/vishvananda/netlink"

	"github.com/telekom/BOOTy/pkg/network"
)

// UnderlayTier manages eBGP peering with leaf switches.
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

// Setup creates the underlay loopback, detects NICs, starts the BGP server,
// and adds peers for each physical interface.
func (u *UnderlayTier) Setup(ctx context.Context) error {
	if err := u.createUnderlayDummy(); err != nil {
		return fmt.Errorf("create underlay dummy: %w", err)
	}

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

	if err := enableForwarding(u.log); err != nil {
		return fmt.Errorf("enable forwarding: %w", err)
	}

	if err := u.startBgpServer(ctx); err != nil {
		return fmt.Errorf("start BGP server: %w", err)
	}

	if err := u.addPeers(ctx); err != nil {
		return fmt.Errorf("add BGP peers: %w", err)
	}

	return nil
}

// Ready waits until at least one BGP peer reaches ESTABLISHED state.
func (u *UnderlayTier) Ready(ctx context.Context, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-deadline:
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
	if err := netlink.LinkAdd(dummy); err != nil && !os.IsExist(err) {
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

	if err := netlink.AddrAdd(link, addr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add addr to dummy.underlay: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up dummy.underlay: %w", err)
	}

	// Assign dummy to VRF for traffic isolation.
	if u.cfg.VRFName != "" {
		vrfLink, err := netlink.LinkByName(u.cfg.VRFName)
		if err != nil {
			u.log.Debug("VRF not yet created, skipping dummy assignment", "vrf", u.cfg.VRFName)
		} else {
			if err := netlink.LinkSetMasterByIndex(link, vrfLink.Attrs().Index); err != nil {
				return fmt.Errorf("assign dummy to VRF %s: %w", u.cfg.VRFName, err)
			}
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
			if err == nil {
				if err := netlink.LinkSetMasterByIndex(link, vrfLink.Attrs().Index); err != nil {
					u.log.Warn("Failed to assign NIC to VRF", "nic", nic, "error", err)
				}
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
	for _, nic := range u.nics {
		if err := u.addInterfacePeer(ctx, nic); err != nil {
			u.log.Warn("Failed to add peer for NIC", "nic", nic, "error", err)
		}
	}
	return nil
}

func (u *UnderlayTier) addInterfacePeer(ctx context.Context, iface string) error {
	timers := &apipb.Timers{
		Config: &apipb.TimersConfig{
			KeepaliveInterval: u.cfg.KeepaliveInterval,
			HoldTime:          u.cfg.HoldTime,
		},
	}

	families := []*apipb.AfiSafi{
		{
			Config: &apipb.AfiSafiConfig{
				Family: &apipb.Family{Afi: apipb.Family_AFI_IP, Safi: apipb.Family_SAFI_UNICAST},
			},
		},
		{
			Config: &apipb.AfiSafiConfig{
				Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
			},
		},
	}

	peer := &apipb.Peer{
		Conf: &apipb.PeerConf{
			NeighborInterface: iface,
			PeerAsn:           0, // External peer, ASN learned via open
		},
		Timers:   timers,
		AfiSafis: families,
		Transport: &apipb.Transport{
			MtuDiscovery: true,
		},
	}

	if err := u.bgp.AddPeer(ctx, &apipb.AddPeerRequest{Peer: peer}); err != nil {
		return fmt.Errorf("add peer on %s: %w", iface, err)
	}

	u.log.Info("Added BGP peer", "interface", iface)
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
