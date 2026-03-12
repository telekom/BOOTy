package network

import (
	"testing"
)

func TestApplyDefaults(t *testing.T) {
	cfg := Config{}
	cfg.ApplyDefaults()

	if cfg.BridgeName != "br.provision" {
		t.Errorf("BridgeName = %q, want %q", cfg.BridgeName, "br.provision")
	}
	if cfg.VRFName != "" {
		t.Errorf("VRFName = %q, want empty (no VRF by default)", cfg.VRFName)
	}
	if cfg.MTU != 9000 {
		t.Errorf("MTU = %d, want %d", cfg.MTU, 9000)
	}
	if cfg.VRFTableID != 1 {
		t.Errorf("VRFTableID = %d, want %d", cfg.VRFTableID, 1)
	}
	if cfg.BFDTransmitMS != 300 {
		t.Errorf("BFDTransmitMS = %d, want %d", cfg.BFDTransmitMS, 300)
	}
	if cfg.BFDReceiveMS != 300 {
		t.Errorf("BFDReceiveMS = %d, want %d", cfg.BFDReceiveMS, 300)
	}
}

func TestApplyDefaults_NoOverwrite(t *testing.T) {
	cfg := Config{
		BridgeName: "custom-br",
		VRFName:    "custom-vrf",
		MTU:        1500,
	}
	cfg.ApplyDefaults()

	if cfg.BridgeName != "custom-br" {
		t.Errorf("BridgeName = %q, want %q", cfg.BridgeName, "custom-br")
	}
	if cfg.VRFName != "custom-vrf" {
		t.Errorf("VRFName = %q, want %q", cfg.VRFName, "custom-vrf")
	}
	if cfg.MTU != 1500 {
		t.Errorf("MTU = %d, want %d", cfg.MTU, 1500)
	}
}

func TestIsFRRMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"underlay_subnet_only", Config{UnderlaySubnet: "10.0.0.0/24"}, false},
		{"asn_only", Config{ASN: 65000}, false},
		{"underlay_subnet_and_asn", Config{UnderlaySubnet: "10.0.0.0/24", ASN: 65000}, true},
		{"underlay_ip_and_asn", Config{UnderlayIP: "10.0.0.1", ASN: 65000}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsFRRMode(); got != tt.want {
				t.Errorf("IsFRRMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStaticMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"with_static_ip", Config{StaticIP: "10.0.0.5/24"}, true},
		{"with_gateway_only", Config{StaticGateway: "10.0.0.1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsStaticMode(); got != tt.want {
				t.Errorf("IsStaticMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBondMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"with_interfaces", Config{BondInterfaces: "eth0,eth1"}, true},
		{"with_bond_mode_only", Config{BondMode: "802.3ad"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsBondMode(); got != tt.want {
				t.Errorf("IsBondMode() = %v, want %v", got, tt.want)
			}
		})
	}
}
