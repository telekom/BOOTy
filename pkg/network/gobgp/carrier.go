//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// carrierWatch ends the session of an unnumbered peer as soon as its
// interface loses carrier and starts it again when the carrier returns.
// FRR does this for directly connected eBGP peers ("bgp
// fast-external-failover", on by default): zebra reports the interface
// down and bgpd stops the peer, so its routes are withdrawn at once
// instead of after the hold time.
type carrierWatch struct {
	bgp   *server.BgpServer
	peers map[string]string // interface -> peer address as added to GoBGP
	down  map[string]bool
	log   *slog.Logger
}

// start subscribes to link updates. Closing done stops the watch.
func (w *carrierWatch) start(done chan struct{}) error {
	updates := make(chan netlink.LinkUpdate, 16)
	if err := netlink.LinkSubscribeWithOptions(updates, done, netlink.LinkSubscribeOptions{
		ErrorCallback: func(err error) { w.log.Warn("link updates", "error", err) },
	}); err != nil {
		return fmt.Errorf("subscribe to link updates: %w", err)
	}
	go func() {
		for u := range updates {
			w.handle(u.Attrs().Name, u.Flags&unix.IFF_LOWER_UP != 0)
		}
	}()
	return nil
}

func (w *carrierWatch) handle(iface string, carrier bool) {
	addr, ok := w.peers[iface]
	if !ok || w.down[iface] == !carrier {
		return
	}
	w.down[iface] = !carrier
	ctx := context.Background()
	if !carrier {
		w.log.Info("uplink lost carrier, stopping its BGP session", "interface", iface, "neighbor", addr)
		if err := w.bgp.DisablePeer(ctx, &apipb.DisablePeerRequest{Address: addr, Communication: "interface down"}); err != nil {
			w.log.Warn("stop BGP session", "neighbor", addr, "error", err)
		}
		return
	}
	w.log.Info("uplink has carrier again, starting its BGP session", "interface", iface, "neighbor", addr)
	if err := w.bgp.EnablePeer(ctx, &apipb.EnablePeerRequest{Address: addr}); err != nil {
		w.log.Warn("start BGP session", "neighbor", addr, "error", err)
	}
}
