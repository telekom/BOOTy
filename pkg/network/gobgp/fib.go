//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// fibDebounce collects bursts of best path changes into one reconcile.
const fibDebounce = 200 * time.Millisecond

// fibWriter installs the IPv4 and IPv6 unicast best paths of the GoBGP
// RIB into one kernel routing table, like zebra does for bgpd (#630 C17).
// The routes carry protocol "bgp", so the writer only replaces and
// deletes its own routes.
type fibWriter struct {
	bgp      *server.BgpServer
	table    int
	localASN uint32
	relax    bool
	peerDev  func(string) string
	log      *slog.Logger

	// installed holds the routes this writer installed, by prefix.
	installed map[string]*netlink.Route
}

func newFIBWriter(bgp *server.BgpServer, table int, localASN uint32, relax bool, peerDev func(string) string) *fibWriter {
	return &fibWriter{
		bgp:       bgp,
		table:     table,
		localASN:  localASN,
		relax:     relax,
		peerDev:   peerDev,
		log:       slog.With("tier", "underlay", "component", "fib"),
		installed: map[string]*netlink.Route{},
	}
}

// run reconciles the kernel table on every best path change until ctx
// ends, then removes the routes it installed.
func (w *fibWriter) run(ctx context.Context) {
	changed := make(chan struct{}, 1)
	notify := func(*apipb.WatchEventResponse) {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	err := w.bgp.WatchEvent(ctx, &apipb.WatchEventRequest{Table: &apipb.WatchEventRequest_Table{
		// A new equal-cost path does not change the best path, so every
		// received update triggers a reconcile too.
		Filters: []*apipb.WatchEventRequest_Table_Filter{
			{Type: apipb.WatchEventRequest_Table_Filter_BEST, Init: true},
			{Type: apipb.WatchEventRequest_Table_Filter_ADJIN},
		},
	}}, notify)
	if err != nil {
		w.log.Error("watch best paths", "error", err)
		return
	}
	defer w.removeAll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(fibDebounce):
		}
		if err := w.reconcile(ctx); err != nil {
			w.log.Warn("FIB update failed", "error", err)
		}
	}
}

func (w *fibWriter) reconcile(ctx context.Context) error {
	want := map[string]*netlink.Route{}
	for _, afi := range []apipb.Family_Afi{apipb.Family_AFI_IP, apipb.Family_AFI_IP6} {
		var dests []*apipb.Destination
		err := w.bgp.ListPath(ctx, &apipb.ListPathRequest{
			TableType: apipb.TableType_GLOBAL,
			Family:    &apipb.Family{Afi: afi, Safi: apipb.Family_SAFI_UNICAST},
		}, func(d *apipb.Destination) { dests = append(dests, d) })
		if err != nil {
			return fmt.Errorf("list paths: %w", err)
		}
		routes, err := selectRoutes(dests, w.localASN, w.relax, w.peerDev)
		if err != nil {
			w.log.Warn("skip paths", "error", err)
		}
		for i := range routes {
			r, err := w.kernelRoute(&routes[i])
			if err != nil {
				w.log.Warn("skip route", "prefix", routes[i].Prefix, "error", err)
				continue
			}
			want[routes[i].Prefix.String()] = r
		}
	}
	for key, r := range want {
		if old, ok := w.installed[key]; ok && sameRoute(old, r) {
			continue
		}
		if err := netlink.RouteReplace(r); err != nil {
			w.log.Warn("install route", "prefix", key, "error", err)
			continue
		}
		w.installed[key] = r
		w.log.Info("installed route", "prefix", key, "table", w.table, "nexthops", len(r.MultiPath))
	}
	for key, r := range w.installed {
		if _, ok := want[key]; ok {
			continue
		}
		if err := netlink.RouteDel(r); err != nil {
			w.log.Warn("remove route", "prefix", key, "error", err)
		}
		delete(w.installed, key)
		w.log.Info("removed route", "prefix", key, "table", w.table)
	}
	return nil
}

func (w *fibWriter) removeAll() {
	for key, r := range w.installed {
		if err := netlink.RouteDel(r); err != nil {
			w.log.Debug("remove route", "prefix", key, "error", err)
		}
	}
	w.installed = map[string]*netlink.Route{}
}

// kernelRoute builds the netlink route. IPv4 prefixes with an IPv6 next
// hop (RFC 5549, unnumbered peers) use RTA_VIA.
func (w *fibWriter) kernelRoute(r *fibRoute) (*netlink.Route, error) {
	dst := &net.IPNet{IP: r.Prefix.Addr().AsSlice(), Mask: net.CIDRMask(r.Prefix.Bits(), r.Prefix.Addr().BitLen())}
	route := &netlink.Route{Dst: dst, Table: w.table, Protocol: unix.RTPROT_BGP}
	for _, nh := range r.Nexthops {
		info := &netlink.NexthopInfo{}
		if nh.Dev != "" {
			link, err := netlink.LinkByName(nh.Dev)
			if err != nil {
				return nil, fmt.Errorf("next hop device %s: %w", nh.Dev, err)
			}
			info.LinkIndex = link.Attrs().Index
		}
		ip := net.IP(nh.Addr.AsSlice())
		if r.Prefix.Addr().Is4() && nh.Addr.Is6() {
			info.Via = &netlink.Via{AddrFamily: nl.FAMILY_V6, Addr: ip}
		} else {
			info.Gw = ip
		}
		route.MultiPath = append(route.MultiPath, info)
	}
	return route, nil
}

func sameRoute(a, b *netlink.Route) bool {
	if len(a.MultiPath) != len(b.MultiPath) {
		return false
	}
	for i := range a.MultiPath {
		x, y := a.MultiPath[i], b.MultiPath[i]
		if x.LinkIndex != y.LinkIndex || !x.Gw.Equal(y.Gw) || (x.Via == nil) != (y.Via == nil) {
			return false
		}
		if x.Via != nil && x.Via.String() != y.Via.String() {
			return false
		}
	}
	return true
}
