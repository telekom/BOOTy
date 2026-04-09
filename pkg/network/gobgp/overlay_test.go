//go:build linux

package gobgp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/vishvananda/netlink"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

// mockFDB records FDB operations for assertion in unit tests.
type mockFDB struct {
	linkName  string
	linkErr   error
	sets      []*netlink.Neigh
	appends   []*netlink.Neigh
	dels      []*netlink.Neigh
	setErr    error
	appendErr error
	delErr    error
}

func (m *mockFDB) LinkByName(name string) (netlink.Link, error) {
	m.linkName = name
	if m.linkErr != nil {
		return nil, m.linkErr
	}
	return &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Index: 42, Name: name}}, nil
}

func (m *mockFDB) NeighSet(n *netlink.Neigh) error {
	m.sets = append(m.sets, n)
	return m.setErr
}

func (m *mockFDB) NeighAppend(n *netlink.Neigh) error {
	m.appends = append(m.appends, n)
	return m.appendErr
}

func (m *mockFDB) NeighDel(n *netlink.Neigh) error {
	m.dels = append(m.dels, n)
	return m.delErr
}

func TestBuildRouteDistinguisher(t *testing.T) {
	tests := []struct {
		name    string
		asn     uint32
		vni     uint32
		wantErr bool
		check   func(t *testing.T, a *apipb.RouteDistinguisherTwoOctetASN, b *apipb.RouteDistinguisherFourOctetASN)
	}{
		{
			name: "2-byte ASN",
			asn:  65000,
			vni:  4000,
			check: func(t *testing.T, two *apipb.RouteDistinguisherTwoOctetASN, _ *apipb.RouteDistinguisherFourOctetASN) {
				t.Helper()
				if two == nil {
					t.Fatal("expected 2-octet RD")
				}
				if two.Admin != 65000 {
					t.Errorf("Admin = %d, want 65000", two.Admin)
				}
				if two.Assigned != 4000 {
					t.Errorf("Assigned = %d, want 4000", two.Assigned)
				}
			},
		},
		{
			name: "4-byte ASN",
			asn:  70000,
			vni:  5000,
			check: func(t *testing.T, _ *apipb.RouteDistinguisherTwoOctetASN, four *apipb.RouteDistinguisherFourOctetASN) {
				t.Helper()
				if four == nil {
					t.Fatal("expected 4-octet RD")
				}
				if four.Admin != 70000 {
					t.Errorf("Admin = %d, want 70000", four.Admin)
				}
				// VNI is masked to 16 bits for 4-octet format.
				if four.Assigned != 5000 {
					t.Errorf("Assigned = %d, want 5000", four.Assigned)
				}
			},
		},
		{
			name: "4-byte ASN truncates large VNI",
			asn:  100000,
			vni:  70000,
			check: func(t *testing.T, _ *apipb.RouteDistinguisherTwoOctetASN, four *apipb.RouteDistinguisherFourOctetASN) {
				t.Helper()
				if four == nil {
					t.Fatal("expected 4-octet RD")
				}
				if four.Assigned != 70000&0xFFFF {
					t.Errorf("Assigned = %d, want %d (truncated)", four.Assigned, 70000&0xFFFF)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd, err := buildRouteDistinguisher(tt.asn, tt.vni)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			msg, err := rd.UnmarshalNew()
			if err != nil {
				t.Fatalf("unmarshal RD: %v", err)
			}

			var gotTwo *apipb.RouteDistinguisherTwoOctetASN
			var gotFour *apipb.RouteDistinguisherFourOctetASN
			switch v := msg.(type) {
			case *apipb.RouteDistinguisherTwoOctetASN:
				gotTwo = v
			case *apipb.RouteDistinguisherFourOctetASN:
				gotFour = v
			default:
				t.Fatalf("unexpected RD type: %T", msg)
			}
			tt.check(t, gotTwo, gotFour)
		})
	}
}

func TestBuildRouteTarget(t *testing.T) {
	tests := []struct {
		name string
		asn  uint32
		vni  uint32
	}{
		{"2-byte ASN", 65000, 4000},
		{"4-byte ASN", 70000, 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := buildRouteTarget(tt.asn, tt.vni)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rt == nil {
				t.Fatal("expected non-nil route target")
			}
		})
	}
}

func TestBuildEVPNType5NLRI(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	// Passing a /32 host route (as advertiseType5 now does).
	nlri, err := buildEVPNType5NLRI(rd, "10.100.0.20/32", "10.0.0.1", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nlri == nil {
		t.Fatal("expected non-nil NLRI")
	}

	// Unmarshal and verify fields.
	msg, err := nlri.UnmarshalNew()
	if err != nil {
		t.Fatalf("unmarshal NLRI: %v", err)
	}
	route, ok := msg.(*apipb.EVPNIPPrefixRoute)
	if !ok {
		t.Fatalf("expected EVPNIPPrefixRoute, got %T", msg)
	}
	if route.IpPrefix != "10.100.0.20" {
		t.Errorf("IpPrefix = %s, want 10.100.0.20", route.IpPrefix)
	}
	if route.IpPrefixLen != 32 {
		t.Errorf("IpPrefixLen = %d, want 32", route.IpPrefixLen)
	}
	if route.GwAddress != "10.0.0.1" {
		t.Errorf("GwAddress = %s, want 10.0.0.1", route.GwAddress)
	}
}

func TestBuildType5PathAttrs(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType5NLRI(rd, "10.100.0.20/32", "10.0.0.1", 4000)
	if err != nil {
		t.Fatalf("build NLRI: %v", err)
	}

	pattrs, err := buildType5PathAttrs(nlri, "10.0.0.1", 65000, 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 3 attributes: origin, mp-reach, ext-communities.
	if len(pattrs) != 3 {
		t.Errorf("got %d path attrs, want 3", len(pattrs))
	}
}

func TestBuildEVPNType5NLRIInvalidIP(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	_, err = buildEVPNType5NLRI(rd, "not-a-cidr", "10.0.0.1", 4000)
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

// mustMarshalPath is a test helper that marshals an NLRI and optional next-hop
// into an apipb.Path for dispatch testing.
func mustMarshalPath(t *testing.T, nlriMsg interface{ ProtoReflect() protoreflect.Message }, nextHop string, withdraw bool) *apipb.Path {
	t.Helper()
	nlri, err := anypb.New(nlriMsg)
	if err != nil {
		t.Fatalf("marshal NLRI: %v", err)
	}
	p := &apipb.Path{
		Nlri:       nlri,
		IsWithdraw: withdraw,
	}
	if nextHop != "" {
		mp, err := anypb.New(&apipb.MpReachNLRIAttribute{
			Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
			NextHops: []string{nextHop},
		})
		if err != nil {
			t.Fatalf("marshal MpReachNLRI: %v", err)
		}
		p.Pattrs = []*anypb.Any{mp}
	}
	return p
}

func TestProcessRouteUpdateDispatch(t *testing.T) {
	// processRouteUpdate should not panic on any NLRI type.
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log: slog.Default(),
		fdb: mock,
	}

	tests := []struct {
		name string
		path *apipb.Path
	}{
		{
			name: "nil NLRI is ignored",
			path: &apipb.Path{},
		},
		{
			name: "type-2 route ignored when EnableL2 false",
			path: mustMarshalPath(t, &apipb.EVPNMACIPAdvertisementRoute{
				MacAddress:  "aa:bb:cc:dd:ee:ff",
				IpAddress:   "10.100.0.50",
				EthernetTag: 0,
			}, "10.0.0.1", false),
		},
		{
			name: "type-3 route ignored when EnableL2 false",
			path: mustMarshalPath(t, &apipb.EVPNInclusiveMulticastEthernetTagRoute{
				IpAddress:   "10.0.0.1",
				EthernetTag: 0,
			}, "10.0.0.1", false),
		},
		{
			name: "type-5 route dispatches to handler",
			path: mustMarshalPath(t, &apipb.EVPNIPPrefixRoute{
				IpPrefix:    "10.100.0.0",
				IpPrefixLen: 24,
				GwAddress:   "10.0.0.1",
			}, "10.0.0.1", false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic.
			overlay.processRouteUpdate(tt.path)
		})
	}
}

func TestExtractNextHop(t *testing.T) {
	tests := []struct {
		name      string
		buildPath func(t *testing.T) *apipb.Path
		want      string
	}{
		{
			name: "with MpReach next-hop",
			buildPath: func(t *testing.T) *apipb.Path {
				t.Helper()
				mp, err := anypb.New(&apipb.MpReachNLRIAttribute{
					Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
					NextHops: []string{"10.0.0.1"},
				})
				if err != nil {
					t.Fatalf("anypb.New MpReachNLRIAttribute: %v", err)
				}
				return &apipb.Path{Pattrs: []*anypb.Any{mp}}
			},
			want: "10.0.0.1",
		},
		{
			name: "no MpReach",
			buildPath: func(t *testing.T) *apipb.Path {
				t.Helper()
				return &apipb.Path{}
			},
			want: "",
		},
		{
			name: "MpReach with empty next-hops",
			buildPath: func(t *testing.T) *apipb.Path {
				t.Helper()
				mp, err := anypb.New(&apipb.MpReachNLRIAttribute{
					Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
				})
				if err != nil {
					t.Fatalf("anypb.New MpReachNLRIAttribute: %v", err)
				}
				return &apipb.Path{Pattrs: []*anypb.Any{mp}}
			},
			want: "",
		},
		{
			name: "multiple next-hops returns first",
			buildPath: func(t *testing.T) *apipb.Path {
				t.Helper()
				mp, err := anypb.New(&apipb.MpReachNLRIAttribute{
					Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
					NextHops: []string{"10.0.0.2", "10.0.0.3"},
				})
				if err != nil {
					t.Fatalf("anypb.New MpReachNLRIAttribute: %v", err)
				}
				return &apipb.Path{Pattrs: []*anypb.Any{mp}}
			},
			want: "10.0.0.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.buildPath(t)
			got := extractNextHop(path)
			if got != tt.want {
				t.Errorf("extractNextHop() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractNextHopMixedAttrs(t *testing.T) {
	// MpReach buried among other attributes.
	origin, err := anypb.New(&apipb.OriginAttribute{Origin: 0})
	if err != nil {
		t.Fatalf("marshal origin attribute: %v", err)
	}
	mp, err := anypb.New(&apipb.MpReachNLRIAttribute{
		Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		NextHops: []string{"10.0.0.5"},
	})
	if err != nil {
		t.Fatalf("marshal mp reach attribute: %v", err)
	}
	path := &apipb.Path{Pattrs: []*anypb.Any{origin, mp}}

	got := extractNextHop(path)
	if got != "10.0.0.5" {
		t.Errorf("extractNextHop() = %q, want 10.0.0.5", got)
	}
}

func TestAddGatewayFDB(t *testing.T) {
	tests := []struct {
		name      string
		gateway   string
		appendErr error
		wantErr   bool
		wantMAC   net.HardwareAddr
	}{
		{
			name:    "success",
			gateway: "10.0.0.1",
			wantMAC: net.HardwareAddr{0, 0, 0, 0, 0, 0},
		},
		{
			name:      "neigh append error",
			gateway:   "10.0.0.1",
			appendErr: errors.New("operation not supported"),
			wantErr:   true,
		},
		{
			name:    "invalid gateway IP",
			gateway: "not-an-ip",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockFDB{}
			mock.appendErr = tt.appendErr
			overlay := &OverlayTier{
				cfg: &Config{ProvisionGateway: tt.gateway, ProvisionVNI: 100},
				log: slog.Default(),
				fdb: mock,
			}
			vxLink := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Index: 42, Name: "vxlan100"}}
			err := overlay.addGatewayFDB(vxLink)
			if (err != nil) != tt.wantErr {
				t.Fatalf("addGatewayFDB() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(mock.appends) != 1 {
				t.Fatalf("expected 1 NeighAppend call, got %d", len(mock.appends))
			}
			neigh := mock.appends[0]
			if neigh.HardwareAddr.String() != tt.wantMAC.String() {
				t.Errorf("BUM FDB MAC = %s, want %s", neigh.HardwareAddr, tt.wantMAC)
			}
			if !neigh.IP.Equal(net.ParseIP(tt.gateway)) {
				t.Errorf("BUM FDB VTEP IP = %s, want %s", neigh.IP, tt.gateway)
			}
			if neigh.LinkIndex != vxLink.Index {
				t.Errorf("BUM FDB LinkIndex = %d, want %d", neigh.LinkIndex, vxLink.Index)
			}
		})
	}
}

func TestParsePrefixRoute(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		prefixLen uint32
		wantStr   string
		wantErr   bool
	}{
		{"default route", "0.0.0.0", 0, "0.0.0.0/0", false},
		{"subnet", "10.100.0.0", 24, "10.100.0.0/24", false},
		{"host route", "10.0.0.1", 32, "10.0.0.1/32", false},
		{"invalid IP", "not-an-ip", 0, "", true},
		{"IPv4 prefix length too large", "10.0.0.0", 33, "", true},
		{"IPv6 prefix length too large", "2001:db8::", 129, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePrefixRoute(tt.prefix, tt.prefixLen)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePrefixRoute() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.String() != tt.wantStr {
				t.Errorf("parsePrefixRoute() = %s, want %s", got, tt.wantStr)
			}
		})
	}
}

func TestHandleType5RouteInvalidPrefix(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision"},
		log: slog.Default(),
		fdb: &mockFDB{},
	}

	route := &apipb.EVPNIPPrefixRoute{
		IpPrefix:    "not-an-ip",
		IpPrefixLen: 0,
	}

	// Should return early due to invalid prefix — no panic.
	overlay.handleType5Route(route, "10.0.0.1", false)
}

func TestHandleType5RouteSelfRoute(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision"},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNIPPrefixRoute{
		IpPrefix:    "10.100.0.0",
		IpPrefixLen: 24,
		GwAddress:   "10.0.0.99",
	}

	// Self-originated route (vtep == RouterID) — should be silently skipped.
	overlay.handleType5Route(route, "10.0.0.99", false)
	// No route operations should have been attempted.
	if len(mock.appends) != 0 || len(mock.sets) != 0 {
		t.Error("self-originated type-5 route should be skipped")
	}
}

func TestHandleType5RouteNoGateway(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision"},
		log: slog.Default(),
		fdb: &mockFDB{},
	}

	route := &apipb.EVPNIPPrefixRoute{
		IpPrefix:    "0.0.0.0",
		IpPrefixLen: 0,
		GwAddress:   "",
	}

	// No gateway and no next-hop — should return early.
	overlay.handleType5Route(route, "", false)
}

// --- Type-2 handler tests ---------------------------------------------------

func TestHandleType2RouteInstallsFDB(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{
		MacAddress: "aa:bb:cc:dd:ee:ff",
	}
	overlay.handleType2Route(route, "10.0.0.1", false)

	if len(mock.sets) != 1 {
		t.Fatalf("expected 1 NeighSet call, got %d", len(mock.sets))
	}
	if mock.sets[0].HardwareAddr.String() != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %s, want aa:bb:cc:dd:ee:ff", mock.sets[0].HardwareAddr)
	}
	if !mock.sets[0].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("VTEP IP = %s, want 10.0.0.1", mock.sets[0].IP)
	}
	if got, ok := overlay.macVTEP.Load("aa:bb:cc:dd:ee:ff"); !ok || got != "10.0.0.1" {
		t.Errorf("macVTEP not tracked")
	}
}

func TestHandleType2RouteSelfSkipped(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{MacAddress: "aa:bb:cc:dd:ee:ff"}
	overlay.handleType2Route(route, "10.0.0.99", false)

	if len(mock.sets) != 0 {
		t.Error("self-originated type-2 route should be skipped")
	}
}

func TestHandleType2RouteWithdraw(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}
	overlay.macVTEP.Store("aa:bb:cc:dd:ee:ff", "10.0.0.1")

	route := &apipb.EVPNMACIPAdvertisementRoute{MacAddress: "aa:bb:cc:dd:ee:ff"}
	overlay.handleType2Route(route, "", true)

	if len(mock.dels) != 1 {
		t.Fatalf("expected 1 NeighDel call, got %d", len(mock.dels))
	}
	if _, ok := overlay.macVTEP.Load("aa:bb:cc:dd:ee:ff"); ok {
		t.Error("macVTEP entry should be removed on withdraw")
	}
}

func TestHandleType2RouteInvalidMAC(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{MacAddress: "not-a-mac"}
	overlay.handleType2Route(route, "10.0.0.1", false)

	if len(mock.sets) != 0 {
		t.Error("invalid MAC should be skipped")
	}
}

// --- Type-3 handler tests ---------------------------------------------------

func TestHandleType3RouteAppendsBUM(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{IpAddress: "10.0.0.1"}
	overlay.handleType3Route(route, "10.0.0.1", false)

	if len(mock.appends) != 1 {
		t.Fatalf("expected 1 NeighAppend call, got %d", len(mock.appends))
	}
	if mock.appends[0].HardwareAddr.String() != "00:00:00:00:00:00" {
		t.Errorf("BUM MAC = %s, want 00:00:00:00:00:00", mock.appends[0].HardwareAddr)
	}
	if !mock.appends[0].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("VTEP IP = %s, want 10.0.0.1", mock.appends[0].IP)
	}
}

func TestHandleType3RouteSelfSkipped(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{IpAddress: "10.0.0.99"}
	overlay.handleType3Route(route, "10.0.0.99", false)

	if len(mock.appends) != 0 {
		t.Error("self-originated type-3 route should be skipped")
	}
}

func TestHandleType3RouteWithdraw(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{IpAddress: "10.0.0.1"}
	overlay.handleType3Route(route, "10.0.0.1", true)

	if len(mock.dels) != 1 {
		t.Fatalf("expected 1 NeighDel call, got %d", len(mock.dels))
	}
}

func TestHandleType3RouteNoVTEP(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg: &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
		log: slog.Default(),
		fdb: mock,
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{IpAddress: ""}
	overlay.handleType3Route(route, "", false)

	if len(mock.appends) != 0 {
		t.Error("type-3 route with no VTEP should be skipped")
	}
}

// --- Type-3 NLRI builder tests -----------------------------------------------

func TestBuildEVPNType3NLRI(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType3NLRI(rd, "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg, err := nlri.UnmarshalNew()
	if err != nil {
		t.Fatalf("unmarshal NLRI: %v", err)
	}
	route, ok := msg.(*apipb.EVPNInclusiveMulticastEthernetTagRoute)
	if !ok {
		t.Fatalf("expected EVPNInclusiveMulticastEthernetTagRoute, got %T", msg)
	}
	if route.IpAddress != "10.0.0.1" {
		t.Errorf("IpAddress = %s, want 10.0.0.1", route.IpAddress)
	}
	if route.EthernetTag != 0 {
		t.Errorf("EthernetTag = %d, want 0", route.EthernetTag)
	}
}

func TestBuildType3PathAttrs(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType3NLRI(rd, "10.0.0.1")
	if err != nil {
		t.Fatalf("build NLRI: %v", err)
	}

	pattrs, err := buildType3PathAttrs(nlri, "10.0.0.1", 65000, 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 4 attributes: origin, mp-reach, ext-communities, pmsi-tunnel.
	if len(pattrs) != 4 {
		t.Errorf("got %d path attrs, want 4", len(pattrs))
	}

	// Verify PMSI tunnel attribute is present and correct.
	pmsiFound := false
	for _, attr := range pattrs {
		msg, err := attr.UnmarshalNew()
		if err != nil {
			continue
		}
		if pmsi, ok := msg.(*apipb.PmsiTunnelAttribute); ok {
			pmsiFound = true
			if pmsi.Type != pmsiTunnelTypeIngressReplication {
				t.Errorf("PMSI tunnel type = %d, want %d", pmsi.Type, pmsiTunnelTypeIngressReplication)
			}
			if pmsi.Label != 4000 {
				t.Errorf("PMSI label = %d, want 4000", pmsi.Label)
			}
		}
	}
	if !pmsiFound {
		t.Error("PMSI tunnel attribute not found in path attrs")
	}
}

func TestBuildType3PathAttrs4ByteASN(t *testing.T) {
	rd, err := buildRouteDistinguisher(70000, 5000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType3NLRI(rd, "10.0.0.1")
	if err != nil {
		t.Fatalf("build NLRI: %v", err)
	}

	pattrs, err := buildType3PathAttrs(nlri, "10.0.0.1", 70000, 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pattrs) != 4 {
		t.Errorf("got %d path attrs, want 4", len(pattrs))
	}
}

// --- Type-2 NLRI builder tests -----------------------------------------------

func TestBuildEVPNType2NLRI(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType2NLRI(rd, "aa:bb:cc:dd:ee:ff", "10.100.0.20", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg, err := nlri.UnmarshalNew()
	if err != nil {
		t.Fatalf("unmarshal NLRI: %v", err)
	}
	route, ok := msg.(*apipb.EVPNMACIPAdvertisementRoute)
	if !ok {
		t.Fatalf("expected EVPNMACIPAdvertisementRoute, got %T", msg)
	}
	if route.MacAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MacAddress = %s, want aa:bb:cc:dd:ee:ff", route.MacAddress)
	}
	if route.IpAddress != "10.100.0.20" {
		t.Errorf("IpAddress = %s, want 10.100.0.20", route.IpAddress)
	}
	if len(route.Labels) != 1 || route.Labels[0] != 4000 {
		t.Errorf("Labels = %v, want [4000]", route.Labels)
	}
	if route.EthernetTag != 0 {
		t.Errorf("EthernetTag = %d, want 0", route.EthernetTag)
	}
}

func TestBuildEVPNType2NLRINoIP(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType2NLRI(rd, "aa:bb:cc:dd:ee:ff", "", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg, err := nlri.UnmarshalNew()
	if err != nil {
		t.Fatalf("unmarshal NLRI: %v", err)
	}
	route, ok := msg.(*apipb.EVPNMACIPAdvertisementRoute)
	if !ok {
		t.Fatalf("expected EVPNMACIPAdvertisementRoute, got %T", msg)
	}
	if route.IpAddress != "" {
		t.Errorf("IpAddress = %s, want empty", route.IpAddress)
	}
}

func TestBuildType2PathAttrs(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType2NLRI(rd, "aa:bb:cc:dd:ee:ff", "10.100.0.20", 4000)
	if err != nil {
		t.Fatalf("build NLRI: %v", err)
	}

	pattrs, err := buildType2PathAttrs(nlri, "10.0.0.1", 65000, 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 3 attributes: origin, mp-reach, ext-communities.
	if len(pattrs) != 3 {
		t.Errorf("got %d path attrs, want 3", len(pattrs))
	}

	// Verify MpReach next-hop is RouterID.
	for _, attr := range pattrs {
		msg, err := attr.UnmarshalNew()
		if err != nil {
			continue
		}
		if mp, ok := msg.(*apipb.MpReachNLRIAttribute); ok {
			if len(mp.NextHops) != 1 || mp.NextHops[0] != "10.0.0.1" {
				t.Errorf("NextHops = %v, want [10.0.0.1]", mp.NextHops)
			}
		}
	}
}

func TestBuildType2PathAttrs4ByteASN(t *testing.T) {
	rd, err := buildRouteDistinguisher(70000, 5000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType2NLRI(rd, "aa:bb:cc:dd:ee:ff", "10.100.0.20", 5000)
	if err != nil {
		t.Fatalf("build NLRI: %v", err)
	}

	pattrs, err := buildType2PathAttrs(nlri, "10.0.0.1", 70000, 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pattrs) != 3 {
		t.Errorf("got %d path attrs, want 3", len(pattrs))
	}
}

// --- advertiseType2 / advertiseType3 unit tests (skip verification) ----------

func TestAdvertiseType2SkipsWhenBridgeMACEmpty(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{BridgeMAC: "", ProvisionIP: "10.100.0.20/24"},
		log: slog.Default(),
	}
	if err := overlay.advertiseType2(context.TODO()); err != nil {
		t.Errorf("expected nil error when BridgeMAC empty, got: %v", err)
	}
}

func TestAdvertiseType2SkipsWhenProvisionIPEmpty(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{BridgeMAC: "aa:bb:cc:dd:ee:ff", ProvisionIP: ""},
		log: slog.Default(),
	}
	if err := overlay.advertiseType2(context.TODO()); err != nil {
		t.Errorf("expected nil error when ProvisionIP empty, got: %v", err)
	}
}

func TestAdvertiseType2InvalidBridgeMAC(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{BridgeMAC: "not-a-mac", ProvisionIP: "10.100.0.20/24"},
		log: slog.Default(),
	}
	err := overlay.advertiseType2(context.TODO())
	if err == nil {
		t.Fatal("expected error for invalid bridge MAC")
	}
}

func TestAdvertiseType2InvalidProvisionIP(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{BridgeMAC: "aa:bb:cc:dd:ee:ff", ProvisionIP: "not-a-cidr", ASN: 65000, ProvisionVNI: 100},
		log: slog.Default(),
	}
	err := overlay.advertiseType2(context.TODO())
	if err == nil {
		t.Fatal("expected error for invalid provision IP")
	}
}

func TestOverlaySetupRejectsUnimplementedTypes(t *testing.T) {
	tests := []struct {
		name        string
		overlayType string
		wantErr     string
	}{
		{"l3vpn not implemented", string(OverlayL3VPN), "not yet implemented"},
		{"unknown type", "invalid", "unknown overlay type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overlay := &OverlayTier{
				cfg: &Config{OverlayType: tt.overlayType},
				log: slog.Default(),
			}
			err := overlay.Setup(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, err) || err.Error() == "" {
				t.Fatal("error should not be empty")
			}
		})
	}
}

func TestOverlaySetupSkipsOverlayNone(t *testing.T) {
	overlay := &OverlayTier{
		cfg: &Config{OverlayType: string(OverlayNone)},
		log: slog.Default(),
	}
	if err := overlay.Setup(context.Background()); err != nil {
		t.Fatalf("overlay none should succeed with no-op: %v", err)
	}
}

func TestUnderlaySetupRejectsUnimplementedAF(t *testing.T) {
	tests := []struct {
		name string
		af   string
	}{
		{"ipv6", string(AFIPv6)},
		{"dual-stack", string(AFDualStack)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := NewUnderlayTier(&Config{UnderlayAF: tt.af})
			err := tier.Setup(context.Background())
			if err == nil {
				t.Fatalf("expected error for underlay AF %q", tt.af)
			}
			if !strings.Contains(err.Error(), "not yet implemented") {
				t.Fatalf("expected 'not yet implemented' error, got: %v", err)
			}
		})
	}
}

func mustPathWithRT(t *testing.T, rts ...*anypb.Any) *apipb.Path {
	t.Helper()
	extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
		Communities: rts,
	})
	if err != nil {
		t.Fatalf("marshal ExtendedCommunitiesAttribute: %v", err)
	}
	return &apipb.Path{Pattrs: []*anypb.Any{extComm}}
}

func mustRT2(t *testing.T, asn, vni uint32) *anypb.Any {
	t.Helper()
	a, err := anypb.New(&apipb.TwoOctetAsSpecificExtended{
		IsTransitive: true,
		SubType:      0x02,
		Asn:          asn,
		LocalAdmin:   vni,
	})
	if err != nil {
		t.Fatalf("marshal TwoOctetAsSpecificExtended: %v", err)
	}
	return a
}

func mustRT4(t *testing.T, asn, vni uint32) *anypb.Any {
	t.Helper()
	a, err := anypb.New(&apipb.FourOctetAsSpecificExtended{
		IsTransitive: true,
		SubType:      0x02,
		Asn:          asn,
		LocalAdmin:   vni & 0xFFFF,
	})
	if err != nil {
		t.Fatalf("marshal FourOctetAsSpecificExtended: %v", err)
	}
	return a
}

func TestMatchesLocalRT(t *testing.T) {
	tests := []struct {
		name     string
		path     *apipb.Path
		localASN uint32
		localVNI uint32
		want     bool
	}{
		{
			name:     "matching 2-byte RT returns true",
			path:     mustPathWithRT(t, mustRT2(t, 65000, 4000)),
			localASN: 65000,
			localVNI: 4000,
			want:     true,
		},
		{
			name:     "non-matching ASN returns false",
			path:     mustPathWithRT(t, mustRT2(t, 65001, 4000)),
			localASN: 65000,
			localVNI: 4000,
			want:     false,
		},
		{
			name:     "non-matching VNI returns false",
			path:     mustPathWithRT(t, mustRT2(t, 65000, 9999)),
			localASN: 65000,
			localVNI: 4000,
			want:     false,
		},
		{
			name:     "path with no extended communities returns false",
			path:     &apipb.Path{},
			localASN: 65000,
			localVNI: 4000,
			want:     false,
		},
		{
			name:     "matching 4-byte RT returns true",
			path:     mustPathWithRT(t, mustRT4(t, 70000, 5000)),
			localASN: 70000,
			localVNI: 5000,
			want:     true,
		},
		{
			name:     "multiple communities one matching returns true",
			path:     mustPathWithRT(t, mustRT2(t, 65001, 4000), mustRT2(t, 65000, 4000)),
			localASN: 65000,
			localVNI: 4000,
			want:     true,
		},
		{
			name:     "multiple communities none matching returns false",
			path:     mustPathWithRT(t, mustRT2(t, 65001, 4000), mustRT2(t, 65002, 4000)),
			localASN: 65000,
			localVNI: 4000,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesLocalRT(tt.path, tt.localASN, tt.localVNI)
			if got != tt.want {
				t.Errorf("matchesLocalRT() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessRouteUpdateRTFilter(t *testing.T) {
	nlri, err := anypb.New(&apipb.EVPNIPPrefixRoute{
		IpPrefix:    "10.100.0.0",
		IpPrefixLen: 24,
		GwAddress:   "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("marshal NLRI: %v", err)
	}

	mp, err := anypb.New(&apipb.MpReachNLRIAttribute{
		Family:   &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		NextHops: []string{"10.0.0.1"},
	})
	if err != nil {
		t.Fatalf("marshal MpReachNLRI: %v", err)
	}

	newOverlay := func(mock *mockFDB) *OverlayTier {
		return &OverlayTier{
			cfg: &Config{
				RouterID:     "10.0.0.99",
				ASN:          65000,
				ProvisionVNI: 4000,
				BridgeName:   "br-test",
			},
			log: slog.Default(),
			fdb: mock,
		}
	}

	t.Run("route with matching RT is dispatched to FDB", func(t *testing.T) {
		mock := &mockFDB{}
		overlay := newOverlay(mock)
		extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
			Communities: []*anypb.Any{mustRT2(t, 65000, 4000)},
		})
		if err != nil {
			t.Fatalf("marshal ExtendedCommunitiesAttribute: %v", err)
		}
		path := &apipb.Path{
			Nlri:   nlri,
			Pattrs: []*anypb.Any{mp, extComm},
		}
		overlay.processRouteUpdate(path)
		// Matching RT must reach FDB dispatch (LinkByName is called for Type-5 installs).
		if mock.linkName == "" {
			t.Error("matching RT: expected FDB dispatch (LinkByName called), but it was not")
		}
	})

	t.Run("route with non-matching RT is skipped", func(t *testing.T) {
		mock := &mockFDB{}
		overlay := newOverlay(mock)
		extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
			Communities: []*anypb.Any{mustRT2(t, 99999, 1111)},
		})
		if err != nil {
			t.Fatalf("marshal ExtendedCommunitiesAttribute: %v", err)
		}
		path := &apipb.Path{
			Nlri:   nlri,
			Pattrs: []*anypb.Any{extComm},
		}
		overlay.processRouteUpdate(path)
		// Non-matching RT must not reach FDB dispatch.
		if mock.linkName != "" {
			t.Errorf("non-matching RT: unexpected FDB dispatch via LinkByName(%q)", mock.linkName)
		}
	})

	t.Run("withdrawal with non-matching RT is not skipped", func(t *testing.T) {
		mock := &mockFDB{}
		overlay := newOverlay(mock)
		extComm, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{
			Communities: []*anypb.Any{mustRT2(t, 99999, 1111)},
		})
		if err != nil {
			t.Fatalf("marshal ExtendedCommunitiesAttribute: %v", err)
		}
		// Withdrawals carry no RT in practice; we must not filter them on RT.
		path := &apipb.Path{
			IsWithdraw: true,
			Nlri:       nlri,
			Pattrs:     []*anypb.Any{extComm},
		}
		overlay.processRouteUpdate(path)
		// Withdrawal must reach FDB dispatch regardless of RT mismatch.
		if mock.linkName == "" {
			t.Error("withdrawal: expected FDB dispatch (LinkByName called) even with RT mismatch, but it was not")
		}
	})
}
