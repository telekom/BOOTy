package gobgp

import (
	"testing"
)

func TestParseUnderlayAF(t *testing.T) {
	tests := []struct {
		input string
		want  UnderlayAF
		err   bool
	}{
		{"ipv4", AFIPv4, false},
		{"ipv6", AFIPv6, false},
		{"dual-stack", AFDualStack, false},
		{"dualstack", AFDualStack, false},
		{"", AFIPv4, false},
		{"banana", "", true},
	}
	for _, tc := range tests {
		got, err := ParseUnderlayAF(tc.input)
		if (err != nil) != tc.err {
			t.Errorf("ParseUnderlayAF(%q) err = %v, wantErr %v", tc.input, err, tc.err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseUnderlayAF(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseOverlayType(t *testing.T) {
	tests := []struct {
		input string
		want  OverlayType
		err   bool
	}{
		{"evpn-vxlan", OverlayEVPNVXLAN, false},
		{"l3vpn", OverlayL3VPN, false},
		{"none", OverlayNone, false},
		{"", OverlayEVPNVXLAN, false},
		{"unknown", "", true},
	}
	for _, tc := range tests {
		got, err := ParseOverlayType(tc.input)
		if (err != nil) != tc.err {
			t.Errorf("ParseOverlayType(%q) err = %v, wantErr %v", tc.input, err, tc.err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseOverlayType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseStandardCommunity(t *testing.T) {
	tests := []struct {
		input string
		asn   uint16
		val   uint16
		err   bool
	}{
		{"65000:100", 65000, 100, false},
		{"0:0", 0, 0, false},
		{"bad", 0, 0, true},
		{"65000", 0, 0, true},
		{"abc:100", 0, 0, true},
		{"100:abc", 0, 0, true},
		{"99999:0", 0, 0, true}, // exceeds uint16
	}
	for _, tc := range tests {
		asn, val, err := ParseStandardCommunity(tc.input)
		if (err != nil) != tc.err {
			t.Errorf("ParseStandardCommunity(%q) err = %v, wantErr %v", tc.input, err, tc.err)
			continue
		}
		if !tc.err {
			if asn != tc.asn || val != tc.val {
				t.Errorf("ParseStandardCommunity(%q) = (%d, %d), want (%d, %d)", tc.input, asn, val, tc.asn, tc.val)
			}
		}
	}
}

func TestValidateCommunities(t *testing.T) {
	tests := []struct {
		name string
		cfg  CommunityConfig
		err  bool
	}{
		{"valid standard", CommunityConfig{Standard: []string{"65000:100"}}, false},
		{"invalid standard", CommunityConfig{Standard: []string{"bad"}}, true},
		{"valid extended", CommunityConfig{Extended: []string{"RT:65000:100"}}, false},
		{"valid extended 4-octet ASN", CommunityConfig{Extended: []string{"RT:4200000001:100"}}, false},
		{"valid extended large value", CommunityConfig{Extended: []string{"RT:65000:100000"}}, false},
		{"invalid extended too few parts", CommunityConfig{Extended: []string{"RT:65000"}}, true},
		{"invalid extended ASN overflow", CommunityConfig{Extended: []string{"RT:9999999999:100"}}, true},
		{"valid large", CommunityConfig{Large: []string{"65000:1:100"}}, false},
		{"invalid large", CommunityConfig{Large: []string{"65000:1"}}, true},
		{"empty", CommunityConfig{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCommunities(&tc.cfg)
			if (err != nil) != tc.err {
				t.Errorf("ValidateCommunities() err = %v, wantErr %v", err, tc.err)
			}
		})
	}
}

func TestGracefulRestartDefaults(t *testing.T) {
	g := &GracefulRestartConfig{}
	g.ApplyDefaults()
	if g.RestartTime != 120 {
		t.Errorf("RestartTime = %d, want 120", g.RestartTime)
	}
}

func TestPolicyConfigTypes(t *testing.T) {
	p := PolicyConfig{
		LocalPref: 200,
		MED:       50,
		ImportCommunities: CommunityConfig{
			Standard: []string{"65000:100"},
		},
	}
	if p.LocalPref != 200 {
		t.Errorf("LocalPref = %d", p.LocalPref)
	}
	if p.MED != 50 {
		t.Errorf("MED = %d", p.MED)
	}
	if len(p.ImportCommunities.Standard) != 1 {
		t.Error("import communities wrong")
	}
}

func TestUnderlayAFConstants(t *testing.T) {
	if string(AFIPv4) != "ipv4" {
		t.Error("AFIPv4")
	}
	if string(AFIPv6) != "ipv6" {
		t.Error("AFIPv6")
	}
	if string(AFDualStack) != "dual-stack" {
		t.Error("AFDualStack")
	}
}

func TestOverlayTypeConstants(t *testing.T) {
	if string(OverlayEVPNVXLAN) != "evpn-vxlan" {
		t.Error("OverlayEVPNVXLAN")
	}
	if string(OverlayL3VPN) != "l3vpn" {
		t.Error("OverlayL3VPN")
	}
	if string(OverlayNone) != "none" {
		t.Error("OverlayNone")
	}
}
