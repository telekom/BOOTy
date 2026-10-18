//go:build linux

package gobgp

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
)

func peerEvent(addr string, s apipb.PeerState_SessionState, a apipb.PeerState_AdminState) *apipb.Peer {
	return &apipb.Peer{State: &apipb.PeerState{NeighborAddress: addr, SessionState: s, AdminState: a}}
}

// A session that ends while the peer stays up restarts every peer of the
// host that is in Idle; a peer that never came up, one that BOOTy
// disabled and one that is no longer Idle stay.
func TestPeerRestarterHandle(t *testing.T) {
	var mu sync.Mutex
	var restarted []string
	r := &peerRestarter{
		peers: func(host string) []string { return []string{host + "%eth0", host + "%eth1"} },
		idle:  func(_ context.Context, addr string) bool { return addr != "fe80::1%eth1" },
		restart: func(_ context.Context, addr string) error {
			mu.Lock()
			defer mu.Unlock()
			restarted = append(restarted, addr)
			return nil
		},
		log:         slog.Default(),
		established: map[string]bool{},
	}
	ctx := context.Background()
	up, idle := apipb.PeerState_ESTABLISHED, apipb.PeerState_IDLE
	steps := []struct {
		event *apipb.Peer
		want  []string
	}{
		{peerEvent("fe80::1", idle, apipb.PeerState_UP), nil},
		{peerEvent("fe80::1", apipb.PeerState_ACTIVE, apipb.PeerState_UP), nil},
		{peerEvent("fe80::1", up, apipb.PeerState_UP), nil},
		{peerEvent("fe80::1", idle, apipb.PeerState_UP), []string{"fe80::1%eth0"}},
		{peerEvent("fe80::1", idle, apipb.PeerState_UP), []string{"fe80::1%eth0"}},
		{peerEvent("fe80::1", up, apipb.PeerState_UP), []string{"fe80::1%eth0"}},
		{peerEvent("fe80::1", idle, apipb.PeerState_DOWN), []string{"fe80::1%eth0"}},
	}
	for i, s := range steps {
		r.handle(ctx, s.event)
		r.wg.Wait()
		if !slices.Equal(restarted, s.want) {
			t.Fatalf("step %d: restarted %v, want %v", i, restarted, s.want)
		}
	}
	r.handle(ctx, peerEvent("fe80::1", up, apipb.PeerState_UP))
	r.stop()
	r.handle(ctx, peerEvent("fe80::1", idle, apipb.PeerState_UP))
	r.wg.Wait()
	if len(restarted) != 1 {
		t.Fatalf("restarted after stop: %v", restarted)
	}
}

// GoBGP keeps the stale routes of a restarting peer that sent family
// tuples; such a peer is not deleted.
func TestKeepsGracefulRestartRoutes(t *testing.T) {
	tuples := []*apipb.AfiSafi{{MpGracefulRestart: &apipb.MpGracefulRestart{State: &apipb.MpGracefulRestartState{Received: true}}}}
	for _, tt := range []struct {
		name string
		p    *apipb.Peer
		want bool
	}{
		{"restarting with tuples", &apipb.Peer{GracefulRestart: &apipb.GracefulRestart{PeerRestarting: true}, AfiSafis: tuples}, true},
		{"restarting without tuples", &apipb.Peer{GracefulRestart: &apipb.GracefulRestart{PeerRestarting: true}, AfiSafis: ipv4Families()}, false},
		{"not restarting", &apipb.Peer{AfiSafis: tuples}, false},
	} {
		if got := keepsGracefulRestartRoutes(tt.p); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

// reestablishAfterLeafReset peers BOOTy (127.0.0.1) with a leaf
// (127.0.0.2) that resets the session like FRR: Idle for 1 s, then it
// connects. It reports whether BOOTy is Established again within limit.
func reestablishAfterLeafReset(t *testing.T, restarter bool, limit time.Duration) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bootyPort, leafPort := freePort(t), freePort(t)
	booty := startServerAt(t, 65001, "10.0.0.1", "127.0.0.1", bootyPort)
	leaf := startServerAt(t, 65002, "10.0.0.2", "127.0.0.2", leafPort)
	timers := func() *apipb.Timers {
		return &apipb.Timers{Config: &apipb.TimersConfig{ConnectRetry: 10, HoldTime: 9, KeepaliveInterval: 3, IdleHoldTimeAfterReset: 1}}
	}
	if err := leaf.AddPeer(ctx, &apipb.AddPeerRequest{Peer: &apipb.Peer{
		Conf:      &apipb.PeerConf{NeighborAddress: "127.0.0.1", PeerAsn: 65001},
		Transport: &apipb.Transport{LocalAddress: "127.0.0.2", RemotePort: uint32(bootyPort)}, //nolint:gosec // test port
		Timers:    timers(), AfiSafis: ipv4Families(),
	}}); err != nil {
		t.Fatal(err)
	}
	u := &UnderlayTier{bgp: booty, log: slog.Default()}
	if err := u.addPeer(ctx, &apipb.Peer{
		Conf:      &apipb.PeerConf{NeighborAddress: "127.0.0.2", PeerAsn: 65002},
		Transport: &apipb.Transport{LocalAddress: "127.0.0.1", RemotePort: uint32(leafPort)}, //nolint:gosec // test port
		Timers:    timers(), AfiSafis: ipv4Families(),
	}); err != nil {
		t.Fatal(err)
	}
	if restarter {
		if err := u.startRestarter(); err != nil {
			t.Fatal(err)
		}
		defer u.stopRestart()
	}
	up, _ := waitEstablished(ctx, booty, "127.0.0.2", 0)
	if up == 0 {
		t.Fatal("the session did not come up")
	}
	// GoBGP keeps the uptime in seconds: reset in a later second than the
	// session came up, so the next session has a later uptime.
	time.Sleep(time.Until(time.Unix(up+1, 0)))
	start := time.Now()
	if err := leaf.ResetPeer(ctx, &apipb.ResetPeerRequest{Address: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	limited, stop := context.WithTimeout(ctx, limit)
	defer stop()
	_, ok := waitEstablished(limited, booty, "127.0.0.2", start.Unix())
	t.Logf("restarter %v: Established again after %v: %v", restarter, time.Since(start), ok)
	return ok
}

// waitEstablished waits until the session to addr is up since the Unix
// time since or later, and returns when it came up; false when ctx ends.
func waitEstablished(ctx context.Context, s *server.BgpServer, addr string, since int64) (int64, bool) {
	for {
		var up int64
		_ = s.ListPeer(ctx, &apipb.ListPeerRequest{Address: addr}, func(p *apipb.Peer) {
			if p.GetState().GetSessionState() == apipb.PeerState_ESTABLISHED {
				up = p.GetTimers().GetState().GetUptime().GetSeconds()
			}
		})
		if up != 0 && up >= since {
			return up, true
		}
		select {
		case <-ctx.Done():
			return 0, false
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// When the leaf resets the session, GoBGP alone closes the leaf's
// connection for 5 s in Idle (osrg/gobgp#3735), and the two sides can
// refuse each other for much longer (19 to 30 s seen); with the restarter
// BOOTy accepts it at once, like FRR (1.7 s seen).
func TestRestarterAcceptsLeafAfterReset(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about 10 s")
	}
	const limit = 4 * time.Second
	if reestablishAfterLeafReset(t, false, limit) {
		t.Errorf("without the restarter BOOTy was Established again within %v; GoBGP should hold it 5 s in Idle", limit)
	}
	if !reestablishAfterLeafReset(t, true, limit) {
		t.Errorf("with the restarter BOOTy was not Established again within %v", limit)
	}
}
