package config

import (
	"strings"
	"testing"
)

// The bm4x-cablecheck profile sends on every NIC unless emitAll is set;
// the networkd default sends only on the emitLLDP NICs.
func TestLLDPEmitAllNICs(t *testing.T) {
	for _, tt := range []struct {
		l    LLDPConfig
		want bool
	}{
		{LLDPConfig{}, false},
		{LLDPConfig{Profile: "networkd"}, false},
		{LLDPConfig{Profile: "networkd", EmitAll: new(true)}, true},
		{LLDPConfig{Profile: "bm4x-cablecheck"}, true},
		{LLDPConfig{Profile: "bm4x-cablecheck", EmitAll: new(false)}, false},
	} {
		if got := tt.l.EmitAllNICs(); got != tt.want {
			t.Errorf("%+v: EmitAllNICs = %v, want %v", tt.l, got, tt.want)
		}
	}
}

func TestValidateLLDPProfile(t *testing.T) {
	for _, p := range []string{"", "networkd", "bm4x-cablecheck"} {
		c := &Config{}
		c.Network.LLDP.Profile = p
		if err := c.Validate(); err != nil {
			t.Errorf("profile %q: %v", p, err)
		}
	}
	c := &Config{}
	c.Network.LLDP.Profile = "lldpd"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), `network.lldp.profile (LLDP_PROFILE) "lldpd"`) {
		t.Fatalf("Validate = %v, want an LLDP profile error", err)
	}
}

// network.lldp is strict: an unknown key fails.
func TestLoadYAMLLLDP(t *testing.T) {
	cfg, err := loadYAML(strings.NewReader("network:\n  lldp:\n    profile: bm4x-cablecheck\n    emitAll: false\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.LLDP.Profile != "bm4x-cablecheck" || cfg.Network.LLDP.EmitAll == nil || *cfg.Network.LLDP.EmitAll {
		t.Fatalf("lldp = %+v", cfg.Network.LLDP)
	}
	if _, err := loadYAML(strings.NewReader("network:\n  lldp:\n    portIDSubtype: mac\n"), true); err == nil {
		t.Fatal("unknown network.lldp key accepted")
	}
}

// The firmware LLDP agent is stopped in check mode unless kept, and in
// other modes only when asked.
func TestDisableNICFirmwareLLDP(t *testing.T) {
	for _, tt := range []struct {
		mode, value string
		want        bool
	}{
		{"check", "", true},
		{"check", "keep", false},
		{"provision", "", false},
		{"", "", false},
		{"provision", "disable", true},
	} {
		c := &Config{Mode: tt.mode}
		c.Network.NICFirmwareLLDP = tt.value
		if got := c.DisableNICFirmwareLLDP(); got != tt.want {
			t.Errorf("mode %q value %q: %v, want %v", tt.mode, tt.value, got, tt.want)
		}
	}
	c := &Config{}
	c.Network.NICFirmwareLLDP = "off"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "network.nicFirmwareLLDP (NIC_FW_LLDP)") {
		t.Fatalf("Validate = %v, want a NIC_FW_LLDP error", err)
	}
}
