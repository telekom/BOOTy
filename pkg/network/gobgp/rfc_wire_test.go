//go:build linux

package gobgp

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	apipb "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/apiutil"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"google.golang.org/protobuf/types/known/anypb"
)

// The tests in this file are the wire evidence of docs/rfc-compliance.md:
// they encode the EVPN routes that BOOTy originates with the GoBGP version
// in go.mod and check the bytes against RFC 7432, RFC 8365 and RFC 9136.

const (
	wireASN      = 65001
	wireVNI      = 3000
	wireRouterID = "192.0.2.17"
	wireMAC      = "02:54:c0:00:02:11"
)

// wirePath converts a path that BOOTy builds into GoBGP's native types,
// as the GoBGP server does in AddPath.
func wirePath(t *testing.T, nlri *anypb.Any, pattrs []*anypb.Any) (*bgp.EVPNNLRI, []bgp.PathAttributeInterface) {
	t.Helper()
	p := &apipb.Path{
		Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		Nlri:   nlri,
		Pattrs: pattrs,
	}
	n, err := apiutil.GetNativeNlri(p)
	if err != nil {
		t.Fatalf("native NLRI: %v", err)
	}
	attrs, err := apiutil.GetNativePathAttributes(p)
	if err != nil {
		t.Fatalf("native path attributes: %v", err)
	}
	evpn, ok := n.(*bgp.EVPNNLRI)
	if !ok {
		t.Fatalf("NLRI is %T, want *bgp.EVPNNLRI", n)
	}
	return evpn, attrs
}

func wireRD(t *testing.T) *anypb.Any {
	t.Helper()
	rd, err := buildRouteDistinguisher(wireASN, wireVNI)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}

// wireLabel is the 3-octet label field that carries the VNI: RFC 8365
// section 5.1.3 puts the whole 24-bit VNI in it, not a 20-bit MPLS label.
func wireLabel(vni uint32) []byte { return []byte{byte(vni >> 16), byte(vni >> 8), byte(vni)} }

func extCommunities(t *testing.T, attrs []bgp.PathAttributeInterface) []bgp.ExtendedCommunityInterface {
	t.Helper()
	for _, a := range attrs {
		if ec, ok := a.(*bgp.PathAttributeExtendedCommunities); ok {
			return ec.Value
		}
	}
	t.Fatal("no extended communities attribute")
	return nil
}

// checkOriginAndNoNextHop checks RFC 4271 section 5.1.1 (the originator sets
// ORIGIN, here IGP) and RFC 4760 section 3 (an UPDATE whose NLRI is only in
// MP_REACH_NLRI should not carry NEXT_HOP).
func checkOriginAndNoNextHop(t *testing.T, attrs []bgp.PathAttributeInterface) {
	t.Helper()
	var origin *bgp.PathAttributeOrigin
	for _, a := range attrs {
		switch v := a.(type) {
		case *bgp.PathAttributeOrigin:
			origin = v
		case *bgp.PathAttributeNextHop:
			t.Errorf("NEXT_HOP attribute %v next to MP_REACH_NLRI", v.Value)
		}
	}
	if origin == nil || origin.Value != bgp.BGP_ORIGIN_ATTR_TYPE_IGP {
		t.Errorf("ORIGIN = %v, want IGP", origin)
	}
}

func mpReachNextHop(t *testing.T, attrs []bgp.PathAttributeInterface) net.IP {
	t.Helper()
	for _, a := range attrs {
		if mp, ok := a.(*bgp.PathAttributeMpReachNLRI); ok {
			return mp.Nexthop
		}
	}
	t.Fatal("no MP_REACH_NLRI attribute")
	return nil
}

// TestRFCWireType5 checks the Type-5 route that BOOTy originates for its
// provision IP: RFC 4271 section 5.1.1 and RFC 4760 section 3 (ORIGIN IGP,
// no NEXT_HOP next to MP_REACH_NLRI), RFC 9136 section 3.1 (length 34, ESI zero, Ethernet Tag
// zero, GW IP zero for the interface-less model of section 4.4.1), RFC 8365
// section 5.1.3 (VNI in the label field, next hop the NVE address) and the
// extended communities of RFC 9135 section 8.1 and RFC 9012 (VXLAN tunnel
// type 8).
func TestRFCWireType5(t *testing.T) {
	nlri, err := buildEVPNType5NLRI(wireRD(t), "198.51.100.10/32", type5DirectGateway, wireVNI)
	if err != nil {
		t.Fatal(err)
	}
	pattrs, err := buildType5PathAttrs(nlri, wireRouterID, wireASN, wireVNI, "", wireMAC)
	if err != nil {
		t.Fatal(err)
	}
	n, attrs := wirePath(t, nlri, pattrs)
	b, err := n.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	// Route type 5, length 34 (RFC 9136 section 3.1, IPv4).
	if b[0] != bgp.EVPN_IP_PREFIX || b[1] != 34 || len(b) != 2+34 {
		t.Fatalf("type/length = %d/%d (%d bytes), want 5/34", b[0], b[1], len(b))
	}
	body := b[2:]
	esi, etag := body[8:18], body[18:22]
	if !bytes.Equal(esi, make([]byte, 10)) {
		t.Errorf("ESI = %x, want all zero (no ESI overlay index)", esi)
	}
	if !bytes.Equal(etag, make([]byte, 4)) {
		t.Errorf("Ethernet Tag = %x, want 0", etag)
	}
	if body[22] != 32 || !bytes.Equal(body[23:27], net.ParseIP("198.51.100.10").To4()) {
		t.Errorf("prefix = %x/%d, want 198.51.100.10/32", body[23:27], body[22])
	}
	if !bytes.Equal(body[27:31], make([]byte, 4)) {
		t.Errorf("GW IP = %x, want zero (RFC 9136 section 3.1: zero when it is no overlay index)", body[27:31])
	}
	if !bytes.Equal(body[31:34], wireLabel(wireVNI)) {
		t.Errorf("label = %x, want VNI %d as 24 bits %x", body[31:34], wireVNI, wireLabel(wireVNI))
	}
	if nh := mpReachNextHop(t, attrs); !nh.Equal(net.ParseIP(wireRouterID)) {
		t.Errorf("next hop = %s, want the VTEP %s (RFC 8365 section 5.1.3)", nh, wireRouterID)
	}
	checkOriginAndNoNextHop(t, attrs)
	var rt, encap, rmac int
	for _, ec := range extCommunities(t, attrs) {
		eb, err := ec.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case eb[0] == 0x00 && eb[1] == 0x02:
			rt++
			if got := binary.BigEndian.Uint16(eb[2:4]); got != wireASN {
				t.Errorf("route target AS = %d, want %d", got, wireASN)
			}
			if got := binary.BigEndian.Uint32(eb[4:8]); got != wireVNI {
				t.Errorf("route target value = %d, want %d", got, wireVNI)
			}
		case eb[0] == 0x03 && eb[1] == 0x0c:
			encap++
			if got := binary.BigEndian.Uint16(eb[6:8]); got != vxlanTunnelType {
				t.Errorf("encapsulation tunnel type = %d, want 8 (VXLAN)", got)
			}
		case eb[0] == 0x06 && eb[1] == 0x03:
			rmac++
			if got := net.HardwareAddr(eb[2:8]).String(); got != wireMAC {
				t.Errorf("router MAC = %s, want %s", got, wireMAC)
			}
		}
	}
	if rt != 1 || encap != 1 || rmac != 1 {
		t.Errorf("extended communities: %d route targets, %d encapsulation, %d router MAC; want 1 each (RFC 9135 section 8.1: a single router MAC)", rt, encap, rmac)
	}
}

// TestRFCWireType3 checks the IMET route of the L2 mode: RFC 7432 section
// 11 (originating router IP, Ethernet Tag 0 for the VLAN-based service of
// RFC 8365 section 5.1.3) and the PMSI Tunnel attribute of section 11.2:
// ingress replication, Leaf Information Required zero, tunnel identifier
// the VTEP, and the VNI as 24 bits in the label field.
func TestRFCWireType3(t *testing.T) {
	nlri, err := buildEVPNType3NLRI(wireRD(t), wireRouterID)
	if err != nil {
		t.Fatal(err)
	}
	pattrs, err := buildType3PathAttrs(nlri, wireRouterID, wireASN, wireVNI, "")
	if err != nil {
		t.Fatal(err)
	}
	n, attrs := wirePath(t, nlri, pattrs)
	b, err := n.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	// RD 8, Ethernet Tag 4, IP length 1, IPv4 4: 17 octets.
	if b[0] != bgp.EVPN_INCLUSIVE_MULTICAST_ETHERNET_TAG || b[1] != 17 {
		t.Fatalf("type/length = %d/%d, want 3/17", b[0], b[1])
	}
	body := b[2:]
	if !bytes.Equal(body[8:12], make([]byte, 4)) {
		t.Errorf("Ethernet Tag = %x, want 0 (RFC 8365 section 5.1.3, VLAN-based service)", body[8:12])
	}
	if body[12] != 32 || !bytes.Equal(body[13:17], net.ParseIP(wireRouterID).To4()) {
		t.Errorf("originating router = %x/%d, want %s", body[13:17], body[12], wireRouterID)
	}
	checkOriginAndNoNextHop(t, attrs)
	var pmsi *bgp.PathAttributePmsiTunnel
	for _, a := range attrs {
		if p, ok := a.(*bgp.PathAttributePmsiTunnel); ok {
			pmsi = p
		}
	}
	if pmsi == nil {
		t.Fatal("no PMSI Tunnel attribute (RFC 7432 section 11.2: MUST carry one)")
	}
	pb, err := pmsi.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	v := pb[3:] // flags, type and length octets of the attribute header
	if v[0] != 0 {
		t.Errorf("PMSI flags = %#x, want 0 (Leaf Information Required MUST be zero)", v[0])
	}
	if v[1] != pmsiTunnelTypeIngressReplication {
		t.Errorf("PMSI tunnel type = %d, want 6 (ingress replication)", v[1])
	}
	if !bytes.Equal(v[2:5], wireLabel(wireVNI)) {
		t.Errorf("PMSI label = %x, want VNI as 24 bits %x", v[2:5], wireLabel(wireVNI))
	}
	if !bytes.Equal(v[5:9], net.ParseIP(wireRouterID).To4()) {
		t.Errorf("PMSI tunnel identifier = %x, want the VTEP %s", v[5:9], wireRouterID)
	}
}

// TestRFCWireType2 checks the MAC/IP route of the L2 mode: RFC 7432
// section 7.2 (MAC length 48, IP length 32), RFC 9135 section 6.1 (length
// 37 with one label, no Label2 for asymmetric IRB) and the VNI in Label1.
func TestRFCWireType2(t *testing.T) {
	nlri, err := buildEVPNType2NLRI(wireRD(t), wireMAC, "198.51.100.10", wireVNI)
	if err != nil {
		t.Fatal(err)
	}
	pattrs, err := buildType2PathAttrs(nlri, wireRouterID, wireASN, wireVNI, "")
	if err != nil {
		t.Fatal(err)
	}
	n, attrs := wirePath(t, nlri, pattrs)
	b, err := n.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != bgp.EVPN_ROUTE_TYPE_MAC_IP_ADVERTISEMENT || b[1] != 37 {
		t.Fatalf("type/length = %d/%d, want 2/37 (RFC 9135 section 6.1)", b[0], b[1])
	}
	body := b[2:]
	if !bytes.Equal(body[8:18], make([]byte, 10)) || !bytes.Equal(body[18:22], make([]byte, 4)) {
		t.Errorf("ESI/Ethernet Tag = %x/%x, want zero (single-homed, VLAN-based service)", body[8:18], body[18:22])
	}
	if body[22] != 48 || net.HardwareAddr(body[23:29]).String() != wireMAC {
		t.Errorf("MAC = %d bits %x, want 48 bits %s", body[22], body[23:29], wireMAC)
	}
	if body[29] != 32 || !bytes.Equal(body[30:34], net.ParseIP("198.51.100.10").To4()) {
		t.Errorf("IP = %d bits %x, want 32 bits 198.51.100.10", body[29], body[30:34])
	}
	if !bytes.Equal(body[34:37], wireLabel(wireVNI)) {
		t.Errorf("Label1 = %x, want VNI as 24 bits %x", body[34:37], wireLabel(wireVNI))
	}
	if nh := mpReachNextHop(t, attrs); !nh.Equal(net.ParseIP(wireRouterID)) {
		t.Errorf("next hop = %s, want the VTEP %s", nh, wireRouterID)
	}
	checkOriginAndNoNextHop(t, attrs)
}
