package gobgp

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/apiutil"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
)

// testPath builds a learned IPv4 path with an RFC 5549 next hop.
func testPath(t *testing.T, neighbor, nexthop string, asPath []uint32, localPref uint32, best bool) *apipb.Path {
	t.Helper()
	nlri := bgp.NewIPAddrPrefix(32, "198.51.100.1")
	attrs := []bgp.PathAttributeInterface{
		bgp.NewPathAttributeOrigin(0),
		bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, asPath)}),
		bgp.NewPathAttributeMpReachNLRI(nexthop, []bgp.AddrPrefixInterface{nlri}),
		bgp.NewPathAttributeLocalPref(localPref),
	}
	p, err := apiutil.NewPath(nlri, false, attrs, testAge)
	if err != nil {
		t.Fatal(err)
	}
	p.Best, p.NeighborIp, p.SourceAsn = best, neighbor, asPath[0]
	return p
}

func devOf(m map[string]string) func(string) string {
	return func(n string) string { return m[n] }
}

// Two uplinks to the same leaf: ECMP over both, with the device of each
// link-local next hop (FRR bgp_ecmp_topo1).
func TestSelectRoutesECMPOverUplinks(t *testing.T) {
	dest := &apipb.Destination{Prefix: "198.51.100.1/32", Paths: []*apipb.Path{
		testPath(t, "fe80::1", "fe80::1", []uint32{65500, 4210000017}, 100, true),
		testPath(t, "fe80::2", "fe80::2", []uint32{65500, 4210000017}, 100, false),
	}}
	devs := devOf(map[string]string{"fe80::1": "ens1f0np0", "fe80::2": "ens1f1np1"})
	got, err := selectRoutes([]*apipb.Destination{dest}, 4210000017, false, devs)
	if err != nil {
		t.Fatal(err)
	}
	want := []fibRoute{{Prefix: netip.MustParsePrefix("198.51.100.1/32"), Nexthops: []fibNexthop{
		{Addr: netip.MustParseAddr("fe80::1"), Dev: "ens1f0np0"},
		{Addr: netip.MustParseAddr("fe80::2"), Dev: "ens1f1np1"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectRoutes() = %+v, want %+v", got, want)
	}
}

// Different AS paths of the same length are ECMP only with
// multipath-relax; a lower local preference never is.
func TestSelectRoutesMultipathRules(t *testing.T) {
	paths := func() []*apipb.Path {
		return []*apipb.Path{
			testPath(t, "fe80::1", "fe80::1", []uint32{65500, 65100}, 100, true),
			testPath(t, "fe80::2", "fe80::2", []uint32{65501, 65100}, 100, false),
			testPath(t, "fe80::3", "fe80::3", []uint32{65500, 65100}, 90, false),
		}
	}
	devs := devOf(map[string]string{"fe80::1": "a", "fe80::2": "b", "fe80::3": "c"})
	for _, tt := range []struct {
		relax bool
		want  int
	}{{false, 1}, {true, 2}} {
		got, err := selectRoutes([]*apipb.Destination{{Prefix: "198.51.100.1/32", Paths: paths()}}, 4210000017, tt.relax, devs)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got[0].Nexthops) != tt.want {
			t.Errorf("relax=%v: routes = %+v, want %d next hops", tt.relax, got, tt.want)
		}
	}
}

// Own paths, filtered paths and destinations without a best path are not
// installed. A link-local next hop without an interface is an error.
func TestSelectRoutesSkips(t *testing.T) {
	own := testPath(t, "", "fe80::1", []uint32{65500}, 100, true)
	filtered := testPath(t, "fe80::1", "fe80::1", []uint32{65500}, 100, true)
	filtered.Filtered = true
	got, err := selectRoutes([]*apipb.Destination{
		{Prefix: "192.0.2.17/32", Paths: []*apipb.Path{own}},
		{Prefix: "198.51.100.1/32", Paths: []*apipb.Path{filtered}},
	}, 4210000017, false, devOf(nil))
	if err != nil || len(got) != 0 {
		t.Fatalf("selectRoutes() = %+v, %v; want no routes", got, err)
	}
	unknown := testPath(t, "fe80::9", "fe80::9", []uint32{65500}, 100, true)
	if _, err := selectRoutes([]*apipb.Destination{{Prefix: "198.51.100.1/32", Paths: []*apipb.Path{unknown}}}, 1, false, devOf(nil)); err == nil {
		t.Fatal("selectRoutes() = nil error, want an error for a link-local next hop without interface")
	}
}

var testAge = time.Unix(0, 0)
