package frr

import (
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/network"
)

func TestDeriveAddresses_DirectUnderlayIP(t *testing.T) {
	cfg := network.Config{
		UnderlayIP: "192.168.4.42",
		IPMIMAC:    "aa:bb:cc:dd:ee:ff",
	}
	underlay, overlay, mac, err := DeriveAddresses(&cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if underlay != "192.168.4.42" {
		t.Errorf("underlay = %q, want %q", underlay, "192.168.4.42")
	}
	if overlay != underlay {
		t.Errorf("overlay = %q, want %q (fallback to underlay)", overlay, underlay)
	}
	if mac != "02:54:cc:dd:ee:ff" {
		t.Errorf("mac = %q, want %q", mac, "02:54:cc:dd:ee:ff")
	}
}

func TestDeriveAddresses_FromIPMIOffset(t *testing.T) {
	cfg := network.Config{
		UnderlaySubnet: "192.168.4.0/24",
		OverlaySubnet:  "10.0.0.0/24",
		IPMISubnet:     "172.30.0.0/24",
		IPMIIP:         "172.30.0.42",
		IPMIMAC:        "aa:bb:cc:dd:ee:ff",
	}
	underlay, overlay, mac, err := DeriveAddresses(&cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if underlay != "192.168.4.42" {
		t.Errorf("underlay = %q, want %q", underlay, "192.168.4.42")
	}
	if overlay != "10.0.0.42" {
		t.Errorf("overlay = %q, want %q", overlay, "10.0.0.42")
	}
	if mac != "02:54:cc:dd:ee:ff" {
		t.Errorf("mac = %q, want %q", mac, "02:54:cc:dd:ee:ff")
	}
}

func TestDeriveAddresses_NoUnderlayInfo(t *testing.T) {
	cfg := network.Config{}
	_, _, _, err := DeriveAddresses(&cfg)
	if err == nil {
		t.Fatal("expected error for empty config")
	}
	if !strings.Contains(err.Error(), "underlay IP or") {
		t.Errorf("error = %q, want to contain 'underlay IP or'", err.Error())
	}
}

func TestDeriveAddresses_DefaultBridgeMAC(t *testing.T) {
	cfg := network.Config{UnderlayIP: "10.0.0.1"}
	_, _, mac, err := DeriveAddresses(&cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mac != "02:54:00:00:00:01" {
		t.Errorf("mac = %q, want default %q", mac, "02:54:00:00:00:01")
	}
}

func TestDeriveAddresses_InvalidSourceIP(t *testing.T) {
	cfg := network.Config{
		UnderlaySubnet: "192.168.4.0/24",
		IPMISubnet:     "172.30.0.0/24",
		IPMIIP:         "not-an-ip",
	}
	_, _, _, err := DeriveAddresses(&cfg)
	if err == nil {
		t.Fatal("expected error for invalid source IP")
	}
}

func TestDeriveAddresses_InvalidSubnet(t *testing.T) {
	cfg := network.Config{
		UnderlaySubnet: "bad-subnet",
		IPMISubnet:     "172.30.0.0/24",
		IPMIIP:         "172.30.0.1",
	}
	_, _, _, err := DeriveAddresses(&cfg)
	if err == nil {
		t.Fatal("expected error for invalid subnet")
	}
}

func TestDeriveAddresses_InvalidOverlaySubnet(t *testing.T) {
	cfg := network.Config{
		UnderlayIP:    "10.0.0.1",
		OverlaySubnet: "bad",
		IPMISubnet:    "172.30.0.0/24",
		IPMIIP:        "172.30.0.1",
	}
	_, _, _, err := DeriveAddresses(&cfg)
	if err == nil {
		t.Fatal("expected error for invalid overlay subnet")
	}
}

func TestDeriveBridgeMAC(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"standard", "aa:bb:cc:dd:ee:ff", "02:54:cc:dd:ee:ff"},
		{"dashes", "aa-bb-cc-dd-ee-ff", "02:54:cc:dd:ee:ff"},
		{"too_short", "aa:bb", "02:54:00:00:00:01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveBridgeMAC(tt.input)
			if got != tt.want {
				t.Errorf("DeriveBridgeMAC(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDeriveIPFromOffset(t *testing.T) {
	tests := []struct {
		name      string
		srcIP     string
		srcSubnet string
		tgtSubnet string
		want      string
	}{
		{"offset_42", "172.30.0.42", "172.30.0.0/24", "192.168.4.0/24", "192.168.4.42"},
		{"offset_1", "172.30.0.1", "172.30.0.0/24", "10.0.0.0/24", "10.0.0.1"},
		{"offset_254", "172.30.0.254", "172.30.0.0/24", "10.0.0.0/24", "10.0.0.254"},
		{"zero_offset", "172.30.0.0", "172.30.0.0/24", "10.0.0.0/24", "10.0.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveIPFromOffset(tt.srcIP, tt.srcSubnet, tt.tgtSubnet)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("DeriveIPFromOffset() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveIPFromOffset_Errors(t *testing.T) {
	tests := []struct {
		name      string
		srcIP     string
		srcSubnet string
		tgtSubnet string
	}{
		{"bad_ip", "not-an-ip", "10.0.0.0/24", "10.0.0.0/24"},
		{"bad_src_subnet", "10.0.0.1", "bad", "10.0.0.0/24"},
		{"bad_tgt_subnet", "10.0.0.1", "10.0.0.0/24", "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeriveIPFromOffset(tt.srcIP, tt.srcSubnet, tt.tgtSubnet)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRenderConfig_Basic(t *testing.T) {
	cfg := network.Config{
		ASN:     65000,
		VRFName: "Vrf_underlay",
	}
	conf, err := RenderConfig(&cfg, "192.168.4.42", []string{"eth0", "eth1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		"router bgp 65000 vrf Vrf_underlay",
		"bgp router-id 192.168.4.42",
		"neighbor eth0 interface peer-group fabric",
		"neighbor eth1 interface peer-group fabric",
		"advertise-all-vni",
		"frr defaults datacenter",
	}
	for _, check := range checks {
		if !strings.Contains(conf, check) {
			t.Errorf("config missing %q", check)
		}
	}
}

func TestRenderConfig_Onefabric(t *testing.T) {
	cfg := network.Config{
		ASN:              65000,
		VRFName:          "Vrf_underlay",
		DCGWIPs:          "10.0.0.1,10.0.0.2",
		LeafASN:          65001,
		LocalASN:         65002,
		OverlayAggregate: "10.10.0.0/16",
		VPNRT:            "65000:100",
	}
	conf, err := RenderConfig(&cfg, "192.168.4.42", []string{"eth0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		"neighbor 10.0.0.1 remote-as internal",
		"neighbor 10.0.0.2 remote-as internal",
		"aggregate-address 10.10.0.0/16",
		"route-target both 65000:100",
	}
	for _, check := range checks {
		if !strings.Contains(conf, check) {
			t.Errorf("onefabric config missing %q", check)
		}
	}
}

func TestRenderConfig_NoNICs(t *testing.T) {
	cfg := network.Config{
		ASN:     65000,
		VRFName: "Vrf_test",
	}
	conf, err := RenderConfig(&cfg, "10.0.0.1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(conf, "router bgp 65000 vrf Vrf_test") {
		t.Error("config missing router bgp line")
	}
}
