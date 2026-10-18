//go:build linux

package gobgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	apipb "github.com/osrg/gobgp/v3/api"
	"google.golang.org/protobuf/types/known/anypb"
)

// esTest drives the remote ES handling against the netlink mock.
type esTest struct {
	t    *testing.T
	o    *OverlayTier
	mock *mockOverlayNetlinkOps
	esi  *apipb.EthernetSegmentIdentifier
}

func newESTest(t *testing.T) *esTest {
	t.Helper()
	mock := &mockOverlayNetlinkOps{}
	return &esTest{
		t:    t,
		mock: mock,
		esi:  &apipb.EthernetSegmentIdentifier{Value: []byte{0, 0x11, 0x22, 0x33, 0x44, 0x55, 0, 0, 1}},
		o: &OverlayTier{
			cfg:        &Config{RouterID: "10.0.0.99", ProvisionVNI: 100, BridgeName: "br.provision", EnableL2: true},
			log:        slog.Default(),
			netlinkOps: mock,
		},
	}
}

func (e *esTest) rd(vtep string) *anypb.Any {
	e.t.Helper()
	n := uint32(1)
	if len(vtep) > 0 {
		n = uint32(vtep[len(vtep)-1])
	}
	a, err := buildRouteDistinguisher(65000, n)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// ead sends an EAD route of the test ES with the given Ethernet Tag.
func (e *esTest) ead(vtep string, tag uint32, singleActive, withdraw bool) {
	e.o.handleType1Route(&apipb.EVPNEthernetAutoDiscoveryRoute{Rd: e.rd(vtep), Esi: e.esi, EthernetTag: tag}, vtep, singleActive, withdraw)
}

// activate sends both EAD routes of a VTEP.
func (e *esTest) activate(vtep string) {
	e.ead(vtep, maxET, false, false)
	e.ead(vtep, 0, false, false)
}

func (e *esTest) mac(vtep string) {
	e.o.handleType2Route(&apipb.EVPNMACIPAdvertisementRoute{Rd: e.rd(vtep), Esi: e.esi, MacAddress: "aa:bb:cc:00:00:01"}, vtep, 0, false)
}

func (e *esTest) installed() string { return e.o.type2FDB["aa:bb:cc:00:00:01"] }

func TestESActivationNeedsBothEADRoutes(t *testing.T) {
	t.Run("EAD-per-ES alone", func(t *testing.T) {
		e := newESTest(t)
		e.mac("10.0.0.1")
		e.ead("10.0.0.1", maxET, false, false)
		if e.installed() != "" || len(e.mock.fdbNhids) != 0 || len(e.mock.sets) != 0 {
			t.Fatalf("installed %q with only the EAD-per-ES route", e.installed())
		}
	})
	t.Run("per-ES is Ethernet Tag MAX-ET only", func(t *testing.T) {
		e := newESTest(t)
		e.mac("10.0.0.1")
		e.ead("10.0.0.1", 5, false, false) // an EAD-per-EVI of a VLAN-aware bundle
		e.ead("10.0.0.1", 0, false, false)
		if e.installed() != "" {
			t.Fatalf("two EAD-per-EVI routes activated the VTEP: %q", e.installed())
		}
	})
}

func TestESReservedAndFilteredRoutes(t *testing.T) {
	t.Run("MAX-ESI is no ES", func(t *testing.T) {
		e := newESTest(t)
		e.esi = &apipb.EthernetSegmentIdentifier{Type: 0xff, Value: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
		e.activate("10.0.0.1")
		e.mac("10.0.0.1")
		if len(e.o.type1Paths) != 0 || e.installed() != "10.0.0.1" {
			t.Fatalf("MAX-ESI: EAD routes %v, installed %q; want no ES and the VTEP entry", e.o.type1Paths, e.installed())
		}
	})
	t.Run("own router ID", func(t *testing.T) {
		e := newESTest(t)
		e.activate("10.0.0.99")
		if len(e.o.type1Paths) != 0 {
			t.Fatalf("own EAD routes recorded: %v", e.o.type1Paths)
		}
	})
	t.Run("IPv6 VTEP", func(t *testing.T) {
		e := newESTest(t)
		e.activate("2001:db8::1")
		if len(e.o.type1Paths) != 0 {
			t.Fatalf("IPv6 VTEP recorded: %v", e.o.type1Paths)
		}
	})
}

func TestESVTEPNexthopsFollowTheGroup(t *testing.T) {
	e := newESTest(t)
	e.activate("10.0.0.1")
	e.activate("10.0.0.2")
	e.mac("10.0.0.1")
	id1 := e.o.vtepNexthops["10.0.0.1"]
	e.ead("10.0.0.1", maxET, false, true)
	if _, ok := e.o.vtepNexthops["10.0.0.1"]; ok || !slices.Contains(e.mock.nexthopDels, id1) {
		t.Fatalf("the nexthop of the VTEP that left is kept: nexthops %v, deletes %v", e.o.vtepNexthops, e.mock.nexthopDels)
	}
}

// RFC 7432 sections 8.4 and 14.1.1: a single-active ES uses only the
// primary PE, the VTEP of the MAC route; a change between a VTEP entry and
// a group entry deletes the old entry first (the kernel refuses to
// replace one kind with the other).
func TestESSingleActiveUsesThePrimaryPE(t *testing.T) {
	e := newESTest(t)
	e.activate("10.0.0.1")
	e.activate("10.0.0.2")
	e.mac("10.0.0.1")
	if _, group := parseNhGroupKey(e.installed()); !group {
		t.Fatalf("all-active: installed %q, want the group", e.installed())
	}
	e.ead("10.0.0.2", maxET, true, false) // one Single-Active flag is enough
	if e.installed() != "10.0.0.1" {
		t.Fatalf("single-active: installed %q, want the primary PE 10.0.0.1", e.installed())
	}
	if len(e.mock.fdbDels) != 1 || len(e.mock.sets) != 1 {
		t.Fatalf("group -> VTEP: group entry deletes %v, VTEP sets %d; want the delete first", e.mock.fdbDels, len(e.mock.sets))
	}
	e.ead("10.0.0.2", maxET, false, false)
	if _, group := parseNhGroupKey(e.installed()); !group || len(e.mock.dels) != 1 {
		t.Fatalf("back to all-active: installed %q, VTEP entry deletes %d", e.installed(), len(e.mock.dels))
	}
}

// Without kernel FDB nexthops (before Linux 5.8) the MAC uses its VTEP.
func TestESGroupFailureFallsBackToTheVTEP(t *testing.T) {
	e := newESTest(t)
	e.mock.nexthopErr = errors.New("not supported")
	e.activate("10.0.0.1")
	e.mac("10.0.0.1")
	if e.installed() != "10.0.0.1" || len(e.o.esGroups) != 0 {
		t.Fatalf("installed %q, groups %v; want the VTEP entry", e.installed(), e.o.esGroups)
	}
}

// A nexthop whose delete failed is kept and deleted later.
func TestESFailedNexthopDeleteIsRetried(t *testing.T) {
	e := newESTest(t)
	e.activate("10.0.0.1")
	e.mac("10.0.0.1")
	g := e.o.esGroups[esiKey(e.esi)].id
	e.mock.nexthopDelErr = errors.New("busy")
	e.ead("10.0.0.1", maxET, false, true)
	if !slices.Contains(e.o.staleNexthops, g) {
		t.Fatalf("failed delete of group %d not kept: %v", g, e.o.staleNexthops)
	}
	e.mock.nexthopDelErr = nil
	e.mock.nexthopDels = nil
	e.activate("10.0.0.2")
	if !slices.Contains(e.mock.nexthopDels, g) || slices.Contains(e.o.staleNexthops, g) {
		t.Fatalf("group %d not deleted at the next change: deletes %v, stale %v", g, e.mock.nexthopDels, e.o.staleNexthops)
	}
}

// Teardown deletes every nexthop (global kernel objects), and a callback
// after Teardown installs nothing.
func TestESTeardownRemovesNexthopsAndStops(t *testing.T) {
	e := newESTest(t)
	e.activate("10.0.0.1")
	e.mac("10.0.0.1")
	ids := []uint32{e.o.esGroups[esiKey(e.esi)].id, e.o.vtepNexthops["10.0.0.1"]}
	if err := e.o.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !slices.Contains(e.mock.nexthopDels, id) {
			t.Errorf("nexthop %d not deleted at teardown: %v", id, e.mock.nexthopDels)
		}
	}
	before := fmt.Sprint(len(e.mock.nexthops), len(e.mock.fdbNhids), len(e.mock.sets))
	e.activate("10.0.0.2")
	e.mac("10.0.0.2")
	if after := fmt.Sprint(len(e.mock.nexthops), len(e.mock.fdbNhids), len(e.mock.sets)); after != before {
		t.Fatalf("callbacks after teardown installed state: %s -> %s", before, after)
	}
}

// The Single-Active flag comes from the ESI Label community of the
// received EAD-per-ES path (RFC 7432 section 7.5).
func TestESSingleActiveFlagFromThePath(t *testing.T) {
	for _, single := range []bool{false, true} {
		e := newESTest(t)
		e.o.cfg.ASN = 65000
		p := mustMarshalPath(t, &apipb.EVPNEthernetAutoDiscoveryRoute{Rd: e.rd("10.0.0.1"), Esi: e.esi, EthernetTag: maxET}, "10.0.0.1", false)
		label, err := anypb.New(&apipb.ESILabelExtended{IsSingleActive: single})
		if err != nil {
			t.Fatal(err)
		}
		ec, err := anypb.New(&apipb.ExtendedCommunitiesAttribute{Communities: []*anypb.Any{mustRT2(t, 65000, 100), label}})
		if err != nil {
			t.Fatal(err)
		}
		p.Pattrs = append(p.Pattrs, ec)
		e.o.processRouteUpdate(p)
		if len(e.o.type1Paths) != 1 || !e.o.esSingleActive(esiKey(e.esi)) == single {
			t.Fatalf("Single-Active %v: EAD routes %+v", single, e.o.type1Paths)
		}
	}
}
