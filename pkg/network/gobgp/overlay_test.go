//go:build linux

package gobgp

import (
	"errors"
	"log/slog"
	"net"
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

	nlri, err := buildEVPNType5NLRI(rd, "10.0.0.1", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nlri == nil {
		t.Fatal("expected non-nil NLRI")
	}
}

func TestBuildType5PathAttrs(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType5NLRI(rd, "10.0.0.1", 4000)
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
	// processRouteUpdate should not panic on any NLRI type. With a mock FDB,
	// we can verify dispatch reaches handlers without needing real netlink.
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
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
			name: "type-2 route dispatches without panic",
			path: mustMarshalPath(t, &apipb.EVPNMACIPAdvertisementRoute{
				MacAddress:  "aa:bb:cc:dd:ee:ff",
				IpAddress:   "10.100.0.50",
				EthernetTag: 0,
			}, "10.0.0.1", false),
		},
		{
			name: "type-3 route dispatches without panic",
			path: mustMarshalPath(t, &apipb.EVPNInclusiveMulticastEthernetTagRoute{
				IpAddress:   "10.0.0.1",
				EthernetTag: 0,
			}, "10.0.0.1", false),
		},
		{
			name: "type-5 route dispatches to default (no panic)",
			path: mustMarshalPath(t, &apipb.EVPNIPPrefixRoute{
				IpPrefix:    "10.100.0.0",
				IpPrefixLen: 24,
			}, "10.0.0.1", false),
		},
		{
			name: "type-2 withdraw dispatches without panic",
			path: mustMarshalPath(t, &apipb.EVPNMACIPAdvertisementRoute{
				MacAddress: "aa:bb:cc:dd:ee:ff",
			}, "10.0.0.1", true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic.
			overlay.processRouteUpdate(tt.path)
		})
	}
}

func TestHandleType2RouteSelfSkip(t *testing.T) {
	// When the next-hop matches our own RouterID, handleType2Route
	// should skip FDB installation entirely (no netlink call).
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{
		MacAddress: "aa:bb:cc:dd:ee:ff",
		IpAddress:  "10.100.0.20",
	}

	// Should return early without error (no netlink.LinkByName call).
	overlay.handleType2Route(route, "10.0.0.99", false)
}

func TestHandleType3RouteSelfSkip(t *testing.T) {
	// When the route IP matches our own RouterID, handleType3Route
	// should skip BUM FDB installation entirely.
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{
		IpAddress:   "10.0.0.99",
		EthernetTag: 0,
	}

	// Should return early without error.
	overlay.handleType3Route(route, "10.0.0.99", false)
}

func TestHandleType2RouteInvalidMAC(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{
		MacAddress: "not-a-mac",
	}

	// Should return early due to invalid MAC.
	overlay.handleType2Route(route, "10.0.0.1", false)
}

func TestHandleType2RouteNoNextHop(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{
		MacAddress: "aa:bb:cc:dd:ee:ff",
	}

	// Should return early due to empty next-hop.
	overlay.handleType2Route(route, "", false)
}

func TestHandleType3RouteNoVTEP(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{
		IpAddress: "",
	}

	// Should return early due to no valid VTEP IP.
	overlay.handleType3Route(route, "", false)
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

func TestHandleType2RouteFDBInstall(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{
		MacAddress: "aa:bb:cc:dd:ee:ff",
		IpAddress:  "10.100.0.50",
	}

	overlay.handleType2Route(route, "10.0.0.1", false)

	if len(mock.sets) != 1 {
		t.Fatalf("expected 1 NeighSet call, got %d", len(mock.sets))
	}
	wantMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	if mock.sets[0].HardwareAddr.String() != wantMAC.String() {
		t.Errorf("FDB MAC = %s, want %s", mock.sets[0].HardwareAddr, wantMAC)
	}
	if !mock.sets[0].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("FDB VTEP = %s, want 10.0.0.1", mock.sets[0].IP)
	}
	if overlay.macVTEP["aa:bb:cc:dd:ee:ff"] != "10.0.0.1" {
		t.Errorf("macVTEP not tracked")
	}
}

func TestHandleType2RouteWithdraw(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: map[string]string{"aa:bb:cc:dd:ee:ff": "10.0.0.1"},
	}

	route := &apipb.EVPNMACIPAdvertisementRoute{
		MacAddress: "aa:bb:cc:dd:ee:ff",
	}

	// Withdraw with empty next-hop — should look up stored VTEP.
	overlay.handleType2Route(route, "", true)

	if len(mock.dels) != 1 {
		t.Fatalf("expected 1 NeighDel call, got %d", len(mock.dels))
	}
	if !mock.dels[0].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("FDB del VTEP = %s, want 10.0.0.1", mock.dels[0].IP)
	}
	if _, ok := overlay.macVTEP["aa:bb:cc:dd:ee:ff"]; ok {
		t.Error("macVTEP should be deleted after withdraw")
	}
}

func TestHandleType3RouteFDBInstall(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{
		IpAddress: "10.0.0.1",
	}

	overlay.handleType3Route(route, "10.0.0.1", false)

	if len(mock.appends) != 1 {
		t.Fatalf("expected 1 NeighAppend call, got %d", len(mock.appends))
	}
	wantMAC := net.HardwareAddr{0, 0, 0, 0, 0, 0}
	if mock.appends[0].HardwareAddr.String() != wantMAC.String() {
		t.Errorf("BUM FDB MAC = %s, want %s", mock.appends[0].HardwareAddr, wantMAC)
	}
	if !mock.appends[0].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("BUM FDB VTEP = %s, want 10.0.0.1", mock.appends[0].IP)
	}
}

func TestHandleType3RouteWithdraw(t *testing.T) {
	mock := &mockFDB{}
	overlay := &OverlayTier{
		cfg:     &Config{RouterID: "10.0.0.99", ProvisionVNI: 100},
		log:     slog.Default(),
		fdb:     mock,
		macVTEP: make(map[string]string),
	}

	route := &apipb.EVPNInclusiveMulticastEthernetTagRoute{
		IpAddress: "10.0.0.1",
	}

	overlay.handleType3Route(route, "10.0.0.1", true)

	if len(mock.dels) != 1 {
		t.Fatalf("expected 1 NeighDel call, got %d", len(mock.dels))
	}
	if !mock.dels[0].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("BUM FDB del VTEP = %s, want 10.0.0.1", mock.dels[0].IP)
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
			if !tt.wantErr && len(mock.appends) == 1 {
				if mock.appends[0].HardwareAddr.String() != tt.wantMAC.String() {
					t.Errorf("BUM FDB MAC = %s, want %s", mock.appends[0].HardwareAddr, tt.wantMAC)
				}
			}
		})
	}
}
