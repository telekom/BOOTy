package vlan

import (
	"testing"
)

func TestConfig_InterfaceName(t *testing.T) {
	c := &Config{Parent: "eth0", ID: 100}
	if c.InterfaceName() != "eth0.100" {
		t.Errorf("got %q", c.InterfaceName())
	}

	c2 := &Config{Parent: "eth0", ID: 100, Name: "mgmt"}
	if c2.InterfaceName() != "mgmt" {
		t.Errorf("got %q", c2.InterfaceName())
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{ID: 100, Parent: "eth0"}, false},
		{"with addr", Config{ID: 100, Parent: "eth0", Address: "10.0.0.1/24"}, false},
		{"with gw", Config{ID: 100, Parent: "eth0", Gateway: "10.0.0.1"}, false},
		{"with mtu", Config{ID: 100, Parent: "eth0", MTU: 9000}, false},
		{"dhcp", Config{ID: 100, Parent: "eth0", DHCP: true}, false},
		{"dhcp and static conflict", Config{ID: 100, Parent: "eth0", DHCP: true, Address: "10.0.0.1/24"}, true},
		{"id too low", Config{ID: 0, Parent: "eth0"}, true},
		{"id too high", Config{ID: 5000, Parent: "eth0"}, true},
		{"no parent", Config{ID: 100}, true},
		{"bad addr", Config{ID: 100, Parent: "eth0", Address: "bad"}, true},
		{"bad gw", Config{ID: 100, Parent: "eth0", Gateway: "bad"}, true},
		{"bad mtu", Config{ID: 100, Parent: "eth0", MTU: 10000}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestTrunkConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     TrunkConfig
		wantErr bool
	}{
		{"valid", TrunkConfig{Parent: "eth0", VLANs: []int{100, 200}}, false},
		{"allow all", TrunkConfig{Parent: "eth0", AllowAll: true}, false},
		{"native", TrunkConfig{Parent: "eth0", VLANs: []int{1, 100}, NativeID: 1}, false},
		{"no parent", TrunkConfig{VLANs: []int{100}}, true},
		{"no vlans", TrunkConfig{Parent: "eth0"}, true},
		{"bad id", TrunkConfig{Parent: "eth0", VLANs: []int{5000}}, true},
		{"dup id", TrunkConfig{Parent: "eth0", VLANs: []int{100, 100}}, true},
		{"bad native", TrunkConfig{Parent: "eth0", VLANs: []int{100}, NativeID: 5000}, true},
		{"native not present", TrunkConfig{Parent: "eth0", VLANs: []int{100, 200}, NativeID: 300}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestMultiConfig_Validate(t *testing.T) {
	valid := MultiConfig{
		VLANs: []Config{
			{ID: 100, Parent: "eth0"},
			{ID: 200, Parent: "eth0"},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid config: %v", err)
	}

	dup := MultiConfig{
		VLANs: []Config{
			{ID: 100, Parent: "eth0"},
			{ID: 100, Parent: "eth0"},
		},
	}
	if err := dup.Validate(); err == nil {
		t.Error("expected duplicate error")
	}

	diffParent := MultiConfig{
		VLANs: []Config{
			{ID: 100, Parent: "eth0"},
			{ID: 100, Parent: "eth1"},
		},
	}
	if err := diffParent.Validate(); err != nil {
		t.Errorf("different parents same ID should be valid: %v", err)
	}

	dupName := MultiConfig{
		VLANs: []Config{
			{ID: 100, Parent: "eth0", Name: "mgmt"},
			{ID: 200, Parent: "eth1", Name: "mgmt"},
		},
	}
	if err := dupName.Validate(); err == nil {
		t.Error("expected duplicate interface name error")
	}
}

func TestMultiConfig_Names(t *testing.T) {
	m := MultiConfig{
		VLANs: []Config{
			{ID: 100, Parent: "eth0"},
			{ID: 200, Parent: "eth0", Name: "mgmt"},
		},
	}
	names := m.Names()
	if len(names) != 2 {
		t.Fatalf("names = %d", len(names))
	}
	if names[0] != "eth0.100" {
		t.Errorf("names[0] = %q", names[0])
	}
	if names[1] != "mgmt" {
		t.Errorf("names[1] = %q", names[1])
	}
}

func TestFormatVLANList(t *testing.T) {
	result := FormatVLANList([]int{100, 200, 300})
	if result != "100, 200, 300" {
		t.Errorf("got %q", result)
	}

	if FormatVLANList(nil) != "" {
		t.Error("nil should be empty")
	}
}

func TestConstants(t *testing.T) {
	if MinID != 1 {
		t.Error("MinID")
	}
	if MaxID != 4094 {
		t.Error("MaxID")
	}
}
