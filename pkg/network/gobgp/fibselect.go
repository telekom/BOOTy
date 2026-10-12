package gobgp

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/apiutil"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
)

// fibRoute is a kernel route that the FIB writer installs: the best path
// and the paths of equal cost (ECMP), like zebra does for bgpd.
type fibRoute struct {
	Prefix   netip.Prefix
	Nexthops []fibNexthop
}

// fibNexthop is one next hop. Dev is set for link-local next hops, which
// are only valid on their interface.
type fibNexthop struct {
	Addr netip.Addr
	Dev  string
}

// pathInfo holds the attributes that decide multipath and the next hop.
type pathInfo struct {
	localPref uint32
	asPath    []uint32
	asLen     int // AS_SET counts as one (RFC 4271 9.1.2.2)
	origin    uint8
	med       uint32
	internal  bool
	nexthop   fibNexthop
}

// selectRoutes returns the kernel routes for the destinations of one
// unicast table. For every destination it takes the best path and every
// other path of equal cost under the FRR multipath rule
// (bgpd/bgp_mpath.c, bgp_path_info_mpath_cmp): same local preference, AS
// path length, origin, MED and peer type, and with multipathRelax unset
// the same AS path ("bgp bestpath as-path multipath-relax"). Paths that
// BOOTy originated itself are skipped. peerDev returns the interface of an
// unnumbered peer address. A path that can not be installed is skipped
// and reported in the error; the other routes are still returned.
func selectRoutes(dests []*apipb.Destination, localASN uint32, multipathRelax bool, peerDev func(string) string) ([]fibRoute, error) {
	var out []fibRoute
	var errs []error
	for _, d := range dests {
		r, err := selectRoute(d, localASN, multipathRelax, peerDev)
		if err != nil {
			errs = append(errs, err)
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, errors.Join(errs...)
}

// selectRoute returns the route of one destination, nil without a usable
// best path.
func selectRoute(d *apipb.Destination, localASN uint32, multipathRelax bool, peerDev func(string) string) (*fibRoute, error) {
	prefix, err := netip.ParsePrefix(d.GetPrefix())
	if err != nil {
		return nil, fmt.Errorf("destination %q: %w", d.GetPrefix(), err)
	}
	var best *pathInfo
	var candidates []*pathInfo
	var errs []error
	for _, p := range d.GetPaths() {
		if p.GetFiltered() || p.GetIsWithdraw() || !learned(p) {
			continue
		}
		info, err := describePath(p, localASN, peerDev)
		if err != nil {
			errs = append(errs, fmt.Errorf("destination %s: %w", prefix, err))
			continue
		}
		if p.GetBest() {
			best = info
		}
		candidates = append(candidates, info)
	}
	if best == nil {
		return nil, errors.Join(errs...)
	}
	r := &fibRoute{Prefix: prefix.Masked()}
	for _, c := range candidates {
		if (c == best || equalCost(best, c, multipathRelax)) && !slices.Contains(r.Nexthops, c.nexthop) {
			r.Nexthops = append(r.Nexthops, c.nexthop)
		}
	}
	slices.SortFunc(r.Nexthops, func(a, b fibNexthop) int {
		if c := a.Addr.Compare(b.Addr); c != 0 {
			return c
		}
		return strings.Compare(a.Dev, b.Dev)
	})
	return r, errors.Join(errs...)
}

// learned reports whether a path came from a peer. Paths that BOOTy
// added itself have no neighbor address.
func learned(p *apipb.Path) bool {
	n := p.GetNeighborIp()
	if n == "" || n == "<nil>" {
		return false
	}
	addr, err := netip.ParseAddr(n)
	return err == nil && !addr.IsUnspecified()
}

func equalCost(a, b *pathInfo, multipathRelax bool) bool {
	if a.localPref != b.localPref || a.asLen != b.asLen ||
		a.origin != b.origin || a.med != b.med || a.internal != b.internal {
		return false
	}
	return multipathRelax || slices.Equal(a.asPath, b.asPath)
}

// read fills the multipath attributes and returns the next hop and the
// link-local next hop.
func (info *pathInfo) read(attrs []bgp.PathAttributeInterface) (nh, ll netip.Addr) {
	for _, a := range attrs {
		switch a := a.(type) {
		case *bgp.PathAttributeLocalPref:
			info.localPref = a.Value
		case *bgp.PathAttributeOrigin:
			info.origin = a.Value
		case *bgp.PathAttributeMultiExitDisc:
			info.med = a.Value
		case *bgp.PathAttributeAsPath:
			for _, seg := range a.Value {
				info.asPath = append(info.asPath, seg.GetAS()...)
				info.asLen += seg.ASLen()
			}
		case *bgp.PathAttributeNextHop:
			nh, _ = netip.AddrFromSlice(a.Value)
		case *bgp.PathAttributeMpReachNLRI:
			nh, _ = netip.AddrFromSlice(a.Nexthop)
			ll, _ = netip.AddrFromSlice(a.LinkLocalNexthop)
		}
	}
	return nh, ll
}

func describePath(p *apipb.Path, localASN uint32, peerDev func(string) string) (*pathInfo, error) {
	attrs, err := apiutil.GetNativePathAttributes(p)
	if err != nil {
		return nil, fmt.Errorf("decode path attributes: %w", err)
	}
	info := &pathInfo{localPref: 100, internal: p.GetSourceAsn() == localASN}
	nh, ll := info.read(attrs)
	// A link-local next hop is preferred, like zebra does for a directly
	// connected peer that sends a global and a link-local address.
	if ll.IsValid() && ll.IsLinkLocalUnicast() {
		nh = ll
	}
	nh = nh.Unmap()
	if !nh.IsValid() || nh.IsUnspecified() {
		return nil, fmt.Errorf("path from %s has no next hop", p.GetNeighborIp())
	}
	info.nexthop = fibNexthop{Addr: nh}
	if nh.IsLinkLocalUnicast() {
		info.nexthop.Dev = peerDev(p.GetNeighborIp())
		if info.nexthop.Dev == "" {
			return nil, fmt.Errorf("link-local next hop %s from %s has no interface", nh, p.GetNeighborIp())
		}
	}
	return info, nil
}
