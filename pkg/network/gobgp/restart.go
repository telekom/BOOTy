//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	apipb "github.com/osrg/gobgp/v3/api"
	"google.golang.org/protobuf/proto"
)

// peerRestarter starts a GoBGP peer again at once when its session ends
// while BOOTy keeps the peer up. GoBGP then holds the peer 5 s in Idle
// (holdtimeIdle, pkg/server/fsm.go) and closes every connection that the
// peer opens meanwhile, so a leaf that ends the session (clear bgp, BFD
// down on the leaf, hold timer) and connects again after FRR's 1 s start
// timer is refused and retries only after its connect timer
// (osrg/gobgp#3735; lab: 36 s against 2.3 s for FRR). FRR accepts
// connections while it waits. A peer that is added again starts in Active
// at once: it accepts the leaf's connection and connects itself after 1 to
// 2 s.
type peerRestarter struct {
	// peers returns the GoBGP peer addresses whose host is addr: the peer
	// state events carry the address without the zone of a link-local
	// peer.
	peers func(addr string) []string
	// idle reports whether the peer is in Idle and stays up: not
	// administratively down (carrier loss, BOOTy stops) and without the
	// routes that GoBGP keeps for a graceful restart of the peer.
	idle func(ctx context.Context, addr string) bool
	// restart deletes the peer and adds it again.
	restart func(ctx context.Context, addr string) error
	log     *slog.Logger

	established map[string]bool // host -> the session was up
	mu          sync.Mutex      // guards stopped and wg.Add
	stopped     bool
	wg          sync.WaitGroup
}

// handle takes a peer state event. GoBGP calls it from one goroutine.
func (r *peerRestarter) handle(ctx context.Context, p *apipb.Peer) {
	host := p.GetState().GetNeighborAddress()
	switch p.GetState().GetSessionState() {
	case apipb.PeerState_ESTABLISHED:
		r.established[host] = true
		return
	case apipb.PeerState_IDLE:
	default:
		return
	}
	if !r.established[host] || p.GetState().GetAdminState() != apipb.PeerState_UP {
		return
	}
	delete(r.established, host)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for _, addr := range r.peers(host) {
			if !r.idle(ctx, addr) {
				continue
			}
			if err := r.restart(ctx, addr); err != nil {
				r.log.Warn("start the BGP peer again", "neighbor", addr, "error", err)
				continue
			}
			r.log.Info("BGP session ended, peer started again at once like FRR", "neighbor", addr)
		}
	}()
}

// stop waits for the restarts in progress; later events are ignored.
func (r *peerRestarter) stop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.wg.Wait()
}

// startRestarter starts the peer restarter until Teardown.
func (u *UnderlayTier) startRestarter() error {
	r := &peerRestarter{
		peers:       u.peersOfHost,
		idle:        u.peerIdleUp,
		restart:     u.restartPeer,
		log:         u.log,
		established: map[string]bool{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := u.bgp.WatchEvent(ctx, &apipb.WatchEventRequest{Peer: &apipb.WatchEventRequest_Peer{}}, func(e *apipb.WatchEventResponse) {
		if pe := e.GetPeer(); pe.GetType() == apipb.WatchEventResponse_PeerEvent_STATE {
			r.handle(ctx, pe.GetPeer())
		}
	})
	if err != nil {
		cancel()
		return fmt.Errorf("watch BGP peer states: %w", err)
	}
	u.stopRestart = func() {
		cancel()
		r.stop()
	}
	return nil
}

// peersOfHost returns the added peers whose address without zone is host.
func (u *UnderlayTier) peersOfHost(host string) []string {
	u.peerOps.Lock()
	defer u.peerOps.Unlock()
	var out []string
	for addr := range u.peerConfs {
		if hostOf(addr) == host {
			out = append(out, addr)
		}
	}
	return out
}

func (u *UnderlayTier) peerIdleUp(ctx context.Context, addr string) bool {
	up := false
	_ = u.bgp.ListPeer(ctx, &apipb.ListPeerRequest{Address: addr}, func(p *apipb.Peer) {
		up = p.GetState().GetSessionState() == apipb.PeerState_IDLE && p.GetState().GetAdminState() == apipb.PeerState_UP &&
			!keepsGracefulRestartRoutes(p)
	})
	return up
}

// keepsGracefulRestartRoutes reports whether GoBGP keeps routes of the
// peer as stale for its graceful restart: the peer restarts and sent
// address family tuples. Deleting the peer would drop them.
func keepsGracefulRestartRoutes(p *apipb.Peer) bool {
	if !p.GetGracefulRestart().GetPeerRestarting() {
		return false
	}
	for _, af := range p.GetAfiSafis() {
		if af.GetMpGracefulRestart().GetState().GetReceived() {
			return true
		}
	}
	return false
}

// restartPeer deletes the peer at addr and adds it again as it was added.
func (u *UnderlayTier) restartPeer(ctx context.Context, addr string) error {
	u.peerOps.Lock()
	defer u.peerOps.Unlock()
	conf := u.peerConfs[addr]
	if conf == nil {
		return nil // moved by the relearner meanwhile
	}
	if err := u.bgp.DeletePeer(ctx, &apipb.DeletePeerRequest{Address: addr}); err != nil {
		return fmt.Errorf("delete peer %s: %w", addr, err)
	}
	peer, _ := proto.Clone(conf).(*apipb.Peer)
	if err := u.bgp.AddPeer(ctx, &apipb.AddPeerRequest{Peer: peer}); err != nil {
		return fmt.Errorf("add peer %s: %w", addr, err)
	}
	return nil
}
