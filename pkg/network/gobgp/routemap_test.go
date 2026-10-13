//go:build linux

package gobgp

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/server"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/telekom/BOOTy/pkg/network"
)

var templateMaps = []network.RouteMap{
	{Name: "TAG-FABRIC-IN", Entries: []network.RouteMapEntry{{Seq: 10, SetLocalPreference: new(uint32(90)), SetCommunities: []string{"65169:200"}, SetCommunityAdditive: true}}},
	{Name: "DENY-TAG-FABRIC-OUT", Entries: []network.RouteMapEntry{{Seq: 20}, {Seq: 10, Deny: true, MatchCommunity: "cm-received-fabric"}}},
}

var templateLists = []network.CommunityList{{Name: "cm-received-fabric", Entries: []network.CommunityListEntry{{Communities: []string{"65169:200"}}}}}

// The bm4x template: entries in sequence order, the implicit deny last.
func TestBuildPolicyTemplate(t *testing.T) {
	peers := []policyPeer{{
		name: "fe80::1%eth1", addr: netip.MustParseAddr("fe80::1%eth1"), localAS: 65501, allowASIn: 3,
		routeMaps: []network.SessionRouteMaps{{Family: "ipv4-unicast", In: "TAG-FABRIC-IN", Out: "DENY-TAG-FABRIC-OUT"}},
	}}
	p, err := buildPolicy(peers, templateMaps, templateLists)
	if err != nil {
		t.Fatal(err)
	}
	im := make([]string, 0, len(p.im))
	ex := make([]string, 0, len(p.ex))
	for _, s := range p.im {
		im = append(im, s.Name+":"+s.GetActions().GetRouteAction().String())
	}
	for _, s := range p.ex {
		ex = append(ex, s.Name+":"+s.GetActions().GetRouteAction().String())
	}
	wantIm := "local-as-65501-3:REJECT rm-in-TAG-FABRIC-IN-ipv4-unicast-10:ACCEPT rm-in-TAG-FABRIC-IN-ipv4-unicast-deny:REJECT"
	wantEx := "rm-out-DENY-TAG-FABRIC-OUT-ipv4-unicast-10-0:REJECT rm-out-DENY-TAG-FABRIC-OUT-ipv4-unicast-20:ACCEPT rm-out-DENY-TAG-FABRIC-OUT-ipv4-unicast-deny:REJECT"
	if strings.Join(im, " ") != wantIm || strings.Join(ex, " ") != wantEx {
		t.Fatalf("import %v\nexport %v", im, ex)
	}
	tag := p.im[1].GetActions()
	if tag.GetLocalPref().GetValue() != 90 || tag.GetCommunity().GetType() != apipb.CommunityAction_ADD || tag.GetCommunity().GetCommunities()[0] != "65169:200" {
		t.Fatalf("TAG-FABRIC-IN actions = %v", tag)
	}
	for _, set := range p.sets {
		if set.GetDefinedType() == apipb.DefinedType_NEIGHBOR && set.GetList()[0] != "fe80::1/128" {
			t.Fatalf("neighbor set %s = %v, want the address without zone", set.GetName(), set.GetList())
		}
	}
	if _, err := buildPolicy(peers, templateMaps[:1], nil); err == nil {
		t.Fatal("undefined route-map accepted")
	}
	deny := []network.CommunityList{{Name: "cm-received-fabric", Entries: []network.CommunityListEntry{{Deny: true, Communities: []string{"1:1"}}}}}
	if _, err := buildPolicy(peers, templateMaps, deny); err == nil {
		t.Fatal("community-list deny entry accepted")
	}
}

func TestASOccurrences(t *testing.T) {
	re := regexp.MustCompile(asOccurrences(65501, 4))
	for path, want := range map[string]bool{
		"65500 65501 65501 65501 65501":       true,
		"65501 65501 65501 65501":             true,
		"65500 65501 1 65501 2 65501 3 65501": true,
		"65500 65501 65501 65501":             false,
		"65500 165501 65501 65501 65501":      false,
		"65500 655011 65501 65501 65501":      false,
	} {
		if got := re.MatchString(path); got != want {
			t.Errorf("%q: match %v, want %v", path, got, want)
		}
	}
	if strings.Contains(asOccurrences(1, 2), "_") {
		t.Fatal("GoBGP replaces _ in AS path regular expressions")
	}
}

// Two GoBGP servers: the fabric (AS 65500) sends three routes to BOOTy's
// server (AS 4210000017; the session has local-as 65501 and allowas-in 3)
// with the template import route-map. Mirrors FRR bgp_route_map topo2
// (local preference and additive community) and bgp_as_allow_in (local-as
// loop with allowas-in).
func TestPolicyInGoBGP(t *testing.T) {
	ctx := context.Background()
	port := freePort(t)
	fabric := startServer(t, 65500, "10.0.0.1", port)
	booty := startServer(t, 4210000017, "192.0.2.17", -1)
	addr := "127.0.0.1"
	// The GoBGP peer carries no local-as here: GoBGP would not send paths
	// with the peer AS to it (sender-side loop check). The policy gets the
	// local-as from the session, so the local-as check is tested alone.
	if err := fabric.AddPeer(ctx, &apipb.AddPeerRequest{Peer: &apipb.Peer{
		Conf:      &apipb.PeerConf{NeighborAddress: addr, PeerAsn: 4210000017},
		Transport: &apipb.Transport{PassiveMode: true},
		AfiSafis:  ipv4Families(),
	}}); err != nil {
		t.Fatal(err)
	}
	// A second fabric peer (AS 65502) with only the export route-map.
	port2 := freePort(t)
	other := startServerAt(t, 65502, "10.0.0.2", "127.0.0.2", port2)
	if err := other.AddPeer(ctx, &apipb.AddPeerRequest{Peer: &apipb.Peer{
		Conf:      &apipb.PeerConf{NeighborAddress: addr, PeerAsn: 4210000017},
		Transport: &apipb.Transport{PassiveMode: true},
		AfiSafis:  ipv4Families(),
	}}); err != nil {
		t.Fatal(err)
	}
	u := &UnderlayTier{bgp: booty, log: slog.Default(), cfg: &Config{
		RouteMaps: templateMaps, CommunityLists: templateLists,
		Sessions: []network.BGPSession{
			{Name: addr, RemoteASN: 65500, LocalASN: 65501, AllowASIn: 3,
				RouteMaps: []network.SessionRouteMaps{{Family: "ipv4-unicast", In: "TAG-FABRIC-IN", Out: "DENY-TAG-FABRIC-OUT"}}},
			{Name: "127.0.0.2", RemoteASN: 65502,
				RouteMaps: []network.SessionRouteMaps{{Family: "ipv4-unicast", Out: "DENY-TAG-FABRIC-OUT"}}},
		},
	}}
	for _, peer := range []struct {
		addr string
		asn  uint32
		port int
	}{{addr, 65500, port}, {"127.0.0.2", 65502, port2}} {
		if err := booty.AddPeer(ctx, &apipb.AddPeerRequest{Peer: &apipb.Peer{
			Conf:      &apipb.PeerConf{NeighborAddress: peer.addr, PeerAsn: peer.asn},
			Transport: &apipb.Transport{RemotePort: uint32(peer.port)}, //nolint:gosec // test port
			AfiSafis:  ipv4Families(),
			Timers:    &apipb.Timers{Config: &apipb.TimersConfig{ConnectRetry: 1}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.installPolicies(ctx); err != nil {
		t.Fatal(err)
	}
	for prefix, prepend := range map[string]int{"203.0.113.0/24": 4, "203.0.113.128/25": 3, "198.51.100.0/24": 0} {
		addRoute(t, fabric, prefix, prepend)
	}
	addRoute(t, booty, "192.0.2.17/32", 0)
	got := map[string]*apipb.Path{}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && len(got) < 3 {
		clear(got)
		_ = booty.ListPath(ctx, &apipb.ListPathRequest{TableType: apipb.TableType_GLOBAL, Family: ipv4Families()[0].GetConfig().GetFamily()},
			func(d *apipb.Destination) { got[d.GetPrefix()] = d.GetPaths()[0] })
		time.Sleep(200 * time.Millisecond)
	}
	if _, ok := got["203.0.113.0/24"]; ok {
		t.Error("path with the local-as 4 times imported, want rejected (allowas-in 3)")
	}
	for _, prefix := range []string{"203.0.113.128/25", "198.51.100.0/24"} {
		p, ok := got[prefix]
		if !ok {
			t.Fatalf("%s not imported; have %v", prefix, got)
		}
		lp, comms := pathPolicyAttrs(t, p)
		if lp != 90 || len(comms) != 1 || comms[0] != 65169<<16|200 {
			t.Errorf("%s: local-pref %d communities %v, want 90 and 65169:200", prefix, lp, comms)
		}
	}
	// DENY-TAG-FABRIC-OUT: the tagged fabric routes do not reach the other
	// peer, the own route does.
	var sent []string
	for time.Now().Before(deadline) {
		sent = sent[:0]
		_ = other.ListPath(ctx, &apipb.ListPathRequest{TableType: apipb.TableType_GLOBAL, Family: ipv4Families()[0].GetConfig().GetFamily()},
			func(d *apipb.Destination) { sent = append(sent, d.GetPrefix()) })
		if len(sent) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(time.Second) // let more updates arrive, if any were sent
	sent = sent[:0]
	_ = other.ListPath(ctx, &apipb.ListPathRequest{TableType: apipb.TableType_GLOBAL, Family: ipv4Families()[0].GetConfig().GetFamily()},
		func(d *apipb.Destination) { sent = append(sent, d.GetPrefix()) })
	if len(sent) != 1 || sent[0] != "192.0.2.17/32" {
		t.Errorf("other peer received %v, want only 192.0.2.17/32", sent)
	}
}

func startServerAt(t *testing.T, asn uint32, id, listen string, port int) *server.BgpServer {
	t.Helper()
	s := server.NewBgpServer()
	go s.Serve()
	t.Cleanup(s.Stop)
	if err := s.StartBgp(context.Background(), &apipb.StartBgpRequest{Global: &apipb.Global{
		Asn: asn, RouterId: id, ListenPort: int32(port), ListenAddresses: []string{listen}, //nolint:gosec // test port
	}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close() //nolint:errcheck // test
	return l.Addr().(*net.TCPAddr).Port
}

func startServer(t *testing.T, asn uint32, id string, port int) *server.BgpServer {
	t.Helper()
	s := server.NewBgpServer()
	go s.Serve()
	t.Cleanup(s.Stop)
	g := &apipb.Global{Asn: asn, RouterId: id, ListenPort: int32(port)} //nolint:gosec // test port
	if port > 0 {
		g.ListenAddresses = []string{"127.0.0.1"}
	}
	if err := s.StartBgp(context.Background(), &apipb.StartBgpRequest{Global: g}); err != nil {
		t.Fatal(err)
	}
	return s
}

func addRoute(t *testing.T, s *server.BgpServer, prefix string, prepend int) {
	t.Helper()
	pfx := netip.MustParsePrefix(prefix)
	nlri, _ := anypb.New(&apipb.IPAddressPrefix{Prefix: pfx.Addr().String(), PrefixLen: uint32(pfx.Bits())}) //nolint:gosec // test
	origin, _ := anypb.New(&apipb.OriginAttribute{Origin: 0})
	nh, _ := anypb.New(&apipb.NextHopAttribute{NextHop: "10.0.0.1"})
	attrs := []*anypb.Any{origin, nh}
	if prepend > 0 {
		asns := make([]uint32, prepend)
		for i := range asns {
			asns[i] = 65501
		}
		asp, _ := anypb.New(&apipb.AsPathAttribute{Segments: []*apipb.AsSegment{{Type: apipb.AsSegment_AS_SEQUENCE, Numbers: asns}}})
		attrs = append(attrs, asp)
	}
	if _, err := s.AddPath(context.Background(), &apipb.AddPathRequest{Path: &apipb.Path{
		Family: &apipb.Family{Afi: apipb.Family_AFI_IP, Safi: apipb.Family_SAFI_UNICAST}, Nlri: nlri, Pattrs: attrs,
	}}); err != nil {
		t.Fatalf("add %s: %v", prefix, err)
	}
}

func pathPolicyAttrs(t *testing.T, p *apipb.Path) (lp uint32, comms []uint32) {
	t.Helper()
	for _, a := range p.GetPattrs() {
		m, err := a.UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		switch v := m.(type) {
		case *apipb.LocalPrefAttribute:
			lp = v.GetLocalPref()
		case *apipb.CommunitiesAttribute:
			comms = v.GetCommunities()
		}
	}
	return lp, comms
}

// Mirrors FRR evpn_type5_test_topo1 test_RT_verification_manual: with
// explicit import route targets any of them imports a route, other route
// targets do not; exports carry every export route target.
func TestExplicitRouteTargets(t *testing.T) {
	o := &OverlayTier{cfg: &Config{ASN: 4210000017, ProvisionVNI: 3000, ImportRTs: []string{"65169:3000", "65169:3001"}, ExportRTs: []string{"65169:3000", "4210000017:7"}}}
	path := func(rts ...[2]uint32) *apipb.Path {
		comms := make([]*anypb.Any, 0, len(rts))
		for _, rt := range rts {
			a, err := buildRouteTarget(rt[0], rt[1])
			if err != nil {
				t.Fatal(err)
			}
			comms = append(comms, a)
		}
		ext, _ := anypb.New(&apipb.ExtendedCommunitiesAttribute{Communities: comms})
		return &apipb.Path{Pattrs: []*anypb.Any{ext}}
	}
	for name, tc := range map[string]struct {
		p    *apipb.Path
		want bool
	}{
		"second import RT": {path([2]uint32{65169, 3001}), true},
		"other RT":         {path([2]uint32{65169, 9}), false},
		"auto RT":          {path([2]uint32{4210000017, 3000}), false},
		"no RT":            {path(), false},
	} {
		if got, err := o.importsRoute(tc.p); err != nil || got != tc.want {
			t.Errorf("%s: imports %v (%v), want %v", name, got, err, tc.want)
		}
	}
	nlri, err := buildEVPNType5NLRI(mustRD(t), "2001:db8::/64", "::", 3000)
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := buildType5PathAttrsRTs(nlri, "192.0.2.17", 4210000017, 3000, o.exportRTSpecs(), "")
	if err != nil {
		t.Fatal(err)
	}
	p := &apipb.Path{Pattrs: attrs}
	for _, rt := range [][2]uint32{{65169, 3000}, {4210000017, 7}} {
		if !matchesLocalRT(p, rt[0], rt[1]) {
			t.Errorf("export misses RT %d:%d", rt[0], rt[1])
		}
	}
}

func mustRD(t *testing.T) *anypb.Any {
	t.Helper()
	rd, err := buildRouteDistinguisher(4210000017, 3000)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}
