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
	if cfg.VRFTableID != 1000 {
		t.Errorf("VRFTableID = %d, want %d", cfg.VRFTableID, 1000)
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

func TestIsVLANMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"with_vlans", Config{VLANs: []VLANConfig{{ID: 100, Parent: "eth0"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsVLANMode(); got != tt.want {
				t.Errorf("IsVLANMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseVLANs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []VLANConfig
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"whitespace", "  ", nil, false},
		{
			"single_minimal",
			"200:eno1",
			[]VLANConfig{{ID: 200, Parent: "eno1"}},
			false,
		},
		{
			"single_with_address",
			"200:eno1:10.200.0.42/24",
			[]VLANConfig{{ID: 200, Parent: "eno1", Address: "10.200.0.42/24"}},
			false,
		},
		{
			"single_with_gateway",
			"200:eno1:10.200.0.42/24:10.200.0.1",
			[]VLANConfig{{ID: 200, Parent: "eno1", Address: "10.200.0.42/24", Gateway: "10.200.0.1"}},
			false,
		},
		{
			"multi_vlan",
			"200:eno1:10.200.0.42/24,300:eno2",
			[]VLANConfig{
				{ID: 200, Parent: "eno1", Address: "10.200.0.42/24"},
				{ID: 300, Parent: "eno2"},
			},
			false,
		},
		{"invalid_no_parent", "200", nil, true},
		{"invalid_id_zero", "0:eth0", nil, true},
		{"invalid_id_high", "4095:eth0", nil, true},
		{"invalid_id_text", "abc:eth0", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVLANs(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseVLANs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("ParseVLANs() returned %d configs, want %d", len(got), len(tt.want))
					return
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("ParseVLANs()[%d] = %+v, want %+v", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestIsGoBGPMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"gobgp", Config{NetworkMode: "gobgp"}, true},
		{"other", Config{NetworkMode: "frr"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsGoBGPMode(); got != tt.want {
				t.Errorf("IsGoBGPMode() = %v, want %v", got, tt.want)
			}
		})
	}
}
