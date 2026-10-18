//go:build linux && linux_e2e

package gobgp

import (
	"log/slog"
	"os"
	"syscall"
	"testing"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/vishvananda/netlink"
	"google.golang.org/protobuf/types/known/anypb"
)

// TestESINetlinkAliasing runs the remote ES handling against the kernel: the
// FDB nexthops, the group and the FDB entry with nhid must be accepted, the
// group must follow an EAD withdraw, and the last withdraw must remove the
// entry and the nexthops.
func TestESINetlinkAliasing(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("test requires root (run with sudo)")
	}
	enterTestNetns(t)
	cfg := &Config{RouterID: "192.0.2.10", ProvisionVNI: 4242, BridgeName: "br-esie2e", VXLANName: "vx4242", EnableL2: true}
	_, vxlan := setupType5E2ELinks(t, cfg)
	o := NewOverlayTier(cfg)
	o.log = slog.Default()

	esi := &apipb.EthernetSegmentIdentifier{Value: []byte{0, 0x11, 0x22, 0x33, 0x44, 0x55, 0, 0, 1}}
	vteps := []string{"192.0.2.21", "192.0.2.22"}
	rds := make([]*anypb.Any, len(vteps))
	for i, vtep := range vteps {
		rd, err := buildRouteDistinguisher(65000, uint32(i+1))
		if err != nil {
			t.Fatal(err)
		}
		rds[i] = rd
		for _, tag := range []uint32{maxET, 0} {
			o.handleType1Route(&apipb.EVPNEthernetAutoDiscoveryRoute{Rd: rd, Esi: esi, EthernetTag: tag}, vtep, false)
		}
	}
	o.handleType2Route(&apipb.EVPNMACIPAdvertisementRoute{Rd: rds[0], Esi: esi, MacAddress: "aa:bb:cc:00:00:01"}, vteps[0], 0, false)

	hasMAC := func() bool {
		neighs, err := netlink.NeighList(vxlan.Attrs().Index, syscall.AF_BRIDGE)
		if err != nil {
			t.Fatalf("fdb list: %v", err)
		}
		for _, n := range neighs {
			if n.HardwareAddr.String() == "aa:bb:cc:00:00:01" {
				return true
			}
		}
		return false
	}
	if g := o.esGroups[esiKey(esi)]; len(g.vteps) != 2 || o.type2FDB["aa:bb:cc:00:00:01"] != nhGroupKey(g.id) || !hasMAC() {
		t.Fatalf("group %+v, installed %q, in kernel %v; want the MAC via a group of two VTEPs", g, o.type2FDB["aa:bb:cc:00:00:01"], hasMAC())
	}

	o.handleType1Route(&apipb.EVPNEthernetAutoDiscoveryRoute{Rd: rds[0], Esi: esi, EthernetTag: maxET}, "", true)
	if g := o.esGroups[esiKey(esi)]; len(g.vteps) != 1 || g.vteps[0] != vteps[1] || !hasMAC() {
		t.Fatalf("after the EAD-per-ES withdraw of %s: group %+v, MAC in kernel %v", vteps[0], g, hasMAC())
	}
	o.handleType1Route(&apipb.EVPNEthernetAutoDiscoveryRoute{Rd: rds[1], Esi: esi, EthernetTag: maxET}, "", true)
	if hasMAC() || len(o.esGroups) != 0 || len(o.vtepNexthops) != 0 {
		t.Fatalf("after the last EAD-per-ES withdraw: MAC in kernel %v, groups %v, nexthops %v", hasMAC(), o.esGroups, o.vtepNexthops)
	}
}
