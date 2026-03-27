package netplan

import (
	"testing"
)

func TestParseFRRConfigBytes_Basic(t *testing.T) {
	t.Helper()
	conf := `
frr defaults datacenter
!
router bgp 65101
 bgp router-id 192.168.4.11
 no bgp default ipv4-unicast
 neighbor fabric peer-group
 neighbor fabric remote-as external
 neighbor eth0 interface peer-group fabric
 !
 address-family ipv4 unicast
  neighbor fabric activate
  redistribute connected
 exit-address-family
 !
 address-family l2vpn evpn
  neighbor fabric activate
  advertise-all-vni
 exit-address-family
exit
`
	params, err := ParseFRRConfigBytes([]byte(conf))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}

	if params.ASN != 65101 {
		t.Errorf("ASN = %d, want 65101", params.ASN)
	}
	if params.RouterID != "192.168.4.11" {
		t.Errorf("RouterID = %q, want 192.168.4.11", params.RouterID)
	}
	if len(params.UnnumberedPeers) != 1 || params.UnnumberedPeers[0] != "eth0" {
		t.Errorf("UnnumberedPeers = %v, want [eth0]", params.UnnumberedPeers)
	}
	if !params.EVPN {
		t.Error("EVPN should be true")
	}
	if !params.AdvertiseAllVNI {
		t.Error("AdvertiseAllVNI should be true")
	}
}

func TestParseFRRConfigBytes_MultipleInterfaces(t *testing.T) {
	t.Helper()
	conf := `
router bgp 65100
 bgp router-id 10.0.0.1
 neighbor eth1 interface remote-as external
 neighbor eth2 interface remote-as external
 neighbor eth3 interface remote-as external
 !
 address-family l2vpn evpn
  advertise-all-vni
 exit-address-family
exit
`
	params, err := ParseFRRConfigBytes([]byte(conf))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}

	if params.ASN != 65100 {
		t.Errorf("ASN = %d, want 65100", params.ASN)
	}
	if len(params.UnnumberedPeers) != 3 {
		t.Errorf("UnnumberedPeers count = %d, want 3", len(params.UnnumberedPeers))
	}
}

func TestParseFRRConfigBytes_NumberedPeers(t *testing.T) {
	t.Helper()
	conf := `
router bgp 65200
 bgp router-id 10.0.0.2
 neighbor 10.0.0.1 remote-as 65100
 neighbor 10.0.0.3 remote-as 65100
 !
 address-family ipv4 unicast
  neighbor 10.0.0.1 activate
 exit-address-family
exit
`
	params, err := ParseFRRConfigBytes([]byte(conf))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}

	if len(params.NumberedPeers) != 2 {
		t.Errorf("NumberedPeers = %v, want 2 entries", params.NumberedPeers)
	}
	if params.EVPN {
		t.Error("EVPN should be false — no l2vpn evpn in config")
	}
}

func TestParseFRRConfigBytes_NoEVPN(t *testing.T) {
	t.Helper()
	conf := `
router bgp 65001
 bgp router-id 10.0.0.5
 neighbor eth0 interface remote-as external
 !
 address-family ipv4 unicast
  neighbor eth0 activate
 exit-address-family
exit
`
	params, err := ParseFRRConfigBytes([]byte(conf))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}

	if params.EVPN {
		t.Error("EVPN should be false")
	}
	if params.AdvertiseAllVNI {
		t.Error("AdvertiseAllVNI should be false")
	}
}

func TestParseFRRConfigBytes_SkipsLinkLocal(t *testing.T) {
	t.Helper()
	conf := `
router bgp 65101
 bgp router-id 10.0.0.1
 neighbor 169.254.100.100 remote-as 65170
 neighbor 10.0.0.2 remote-as 65100
exit
`
	params, err := ParseFRRConfigBytes([]byte(conf))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}

	// 169.254.x.x should be filtered out.
	if len(params.NumberedPeers) != 1 {
		t.Errorf("NumberedPeers = %v, want [10.0.0.2]", params.NumberedPeers)
	}
	if len(params.NumberedPeers) > 0 && params.NumberedPeers[0] != "10.0.0.2" {
		t.Errorf("NumberedPeers[0] = %q, want 10.0.0.2", params.NumberedPeers[0])
	}
}

func TestParseFRRConfigBytes_DuplicateNeighbors(t *testing.T) {
	t.Helper()
	// Same interface appears multiple times (e.g. in different address families).
	conf := `
router bgp 65100
 neighbor eth0 interface remote-as external
 neighbor eth0 interface peer-group fabric
 !
 address-family l2vpn evpn
  neighbor eth0 activate
  advertise-all-vni
 exit-address-family
exit
`
	params, err := ParseFRRConfigBytes([]byte(conf))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}

	// Should deduplicate.
	if len(params.UnnumberedPeers) != 1 {
		t.Errorf("UnnumberedPeers = %v, want [eth0] (deduplicated)", params.UnnumberedPeers)
	}
}

func TestParseFRRConfigBytes_Empty(t *testing.T) {
	t.Helper()
	params, err := ParseFRRConfigBytes([]byte(""))
	if err != nil {
		t.Fatalf("ParseFRRConfigBytes: %v", err)
	}
	if params.ASN != 0 {
		t.Errorf("ASN = %d, want 0", params.ASN)
	}
}
