package gobgp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/network"
)

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name    string
		netCfg  *network.Config
		wantErr bool
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name: "minimal_unnumbered",
			netCfg: &network.Config{
				UnderlayIP:   "10.0.0.1",
				ASN:          65000,
				ProvisionVNI: 4000,
				BGPPeerMode:  network.PeerModeUnnumbered,
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.RouterID != "10.0.0.1" {
					t.Errorf("RouterID = %q, want 10.0.0.1", cfg.RouterID)
				}
				if cfg.ASN != 65000 {
					t.Errorf("ASN = %d, want 65000", cfg.ASN)
				}
				if cfg.ProvisionVNI != 4000 {
					t.Errorf("ProvisionVNI = %d, want 4000", cfg.ProvisionVNI)
				}
				if cfg.ListenPort != 179 {
					t.Errorf("ListenPort = %d, want 179 (default)", cfg.ListenPort)
				}
				if cfg.MTU != 9000 {
					t.Errorf("MTU = %d, want 9000 (default)", cfg.MTU)
				}
			},
		},
		{
			name: "dual_with_neighbors",
			netCfg: &network.Config{
				UnderlayIP:   "10.0.0.5",
				ASN:          65000,
				ProvisionVNI: 4000,
				BGPPeerMode:  network.PeerModeDual,
				BGPNeighbors: "10.0.0.1,10.0.0.2",
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.PeerMode != network.PeerModeDual {
					t.Errorf("PeerMode = %q, want dual", cfg.PeerMode)
				}
				if len(cfg.NeighborAddrs) != 2 {
					t.Errorf("NeighborAddrs = %d, want 2", len(cfg.NeighborAddrs))
				}
			},
		},
		{
			name: "overlay_same_as_underlay",
			netCfg: &network.Config{
				UnderlayIP:   "192.168.1.10",
				ASN:          65100,
				ProvisionVNI: 100,
				BGPPeerMode:  network.PeerModeUnnumbered,
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.OverlayIP != "192.168.1.10" {
					t.Errorf("OverlayIP = %q, want same as RouterID", cfg.OverlayIP)
				}
			},
		},
		{
			name: "missing_underlay_ip",
			netCfg: &network.Config{
				ASN:          65000,
				ProvisionVNI: 4000,
			},
			wantErr: true,
		},
		{
			name: "missing_asn",
			netCfg: &network.Config{
				UnderlayIP:   "10.0.0.1",
				ProvisionVNI: 4000,
			},
			wantErr: true,
		},
		{
			name: "numbered_without_neighbors",
			netCfg: &network.Config{
				UnderlayIP:   "10.0.0.1",
				ASN:          65000,
				ProvisionVNI: 4000,
				BGPPeerMode:  network.PeerModeNumbered,
			},
			wantErr: true,
		},
		{
			name: "custom_timers",
			netCfg: &network.Config{
				UnderlayIP:   "10.0.0.1",
				ASN:          65000,
				ProvisionVNI: 4000,
				BGPPeerMode:  network.PeerModeUnnumbered,
				BGPKeepalive: 10,
				BGPHold:      30,
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.KeepaliveInterval != 10 {
					t.Errorf("KeepaliveInterval = %d, want 10", cfg.KeepaliveInterval)
				}
				if cfg.HoldTime != 30 {
					t.Errorf("HoldTime = %d, want 30", cfg.HoldTime)
				}
			},
		},
		{
			name: "underlay_af_and_overlay_type_wired",
			netCfg: &network.Config{
				UnderlayIP:     "10.0.0.1",
				ASN:            65000,
				ProvisionVNI:   4000,
				BGPPeerMode:    network.PeerModeUnnumbered,
				BGPUnderlayAF:  "ipv6",
				BGPOverlayType: "evpn-vxlan",
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.UnderlayAF != "ipv6" {
					t.Errorf("UnderlayAF = %q, want ipv6", cfg.UnderlayAF)
				}
				if cfg.OverlayType != "evpn-vxlan" {
					t.Errorf("OverlayType = %q, want evpn-vxlan", cfg.OverlayType)
				}
			},
		},
		{
			name: "auth_password_flows_from_network_config",
			netCfg: &network.Config{
				UnderlayIP:      "10.0.0.1",
				ASN:             65000,
				ProvisionVNI:    4000,
				BGPPeerMode:     network.PeerModeUnnumbered,
				BGPAuthPassword: "s3cret",
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.AuthPassword != "s3cret" {
					t.Errorf("AuthPassword = %q, want s3cret", cfg.AuthPassword)
				}
			},
		},
		{
			name: "auth_password_empty_by_default",
			netCfg: &network.Config{
				UnderlayIP:   "10.0.0.1",
				ASN:          65000,
				ProvisionVNI: 4000,
				BGPPeerMode:  network.PeerModeUnnumbered,
			},
			check: func(t *testing.T, cfg *Config) {
				t.Helper()
				if cfg.AuthPassword != "" {
					t.Errorf("AuthPassword = %q, want empty (no auth)", cfg.AuthPassword)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NewConfig(tt.netCfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyDefaults()

	if cfg.ListenPort != 179 {
		t.Errorf("ListenPort = %d, want 179", cfg.ListenPort)
	}
	if cfg.KeepaliveInterval != 3 {
		t.Errorf("KeepaliveInterval = %d, want 3", cfg.KeepaliveInterval)
	}
	if cfg.HoldTime != 9 {
		t.Errorf("HoldTime = %d, want 9", cfg.HoldTime)
	}
	if cfg.BridgeName != "br.provision" {
		t.Errorf("BridgeName = %q, want br.provision", cfg.BridgeName)
	}
	if cfg.MTU != 9000 {
		t.Errorf("MTU = %d, want 9000", cfg.MTU)
	}
	if cfg.VRFTableID != 1000 {
		t.Errorf("VRFTableID = %d, want 1000", cfg.VRFTableID)
	}
}

func TestApplyDefaultsVRFTableIDOneOverridden(t *testing.T) {
	cfg := &Config{VRFTableID: 1}
	cfg.ApplyDefaults()

	if cfg.VRFTableID != 1000 {
		t.Errorf("VRFTableID = %d, want 1000 (table 1 conflicts with default)", cfg.VRFTableID)
	}
}

func TestApplyDefaultsPreservesValues(t *testing.T) {
	cfg := &Config{
		ListenPort:        1179,
		KeepaliveInterval: 10,
		HoldTime:          30,
		BridgeName:        "custom-br",
		MTU:               1500,
		VRFTableID:        42,
	}
	cfg.ApplyDefaults()

	if cfg.ListenPort != 1179 {
		t.Errorf("ListenPort = %d, want 1179", cfg.ListenPort)
	}
	if cfg.KeepaliveInterval != 10 {
		t.Errorf("KeepaliveInterval = %d, want 10", cfg.KeepaliveInterval)
	}
	if cfg.HoldTime != 30 {
		t.Errorf("HoldTime = %d, want 30", cfg.HoldTime)
	}
	if cfg.BridgeName != "custom-br" {
		t.Errorf("BridgeName = %q, want custom-br", cfg.BridgeName)
	}
	if cfg.MTU != 1500 {
		t.Errorf("MTU = %d, want 1500", cfg.MTU)
	}
	if cfg.VRFTableID != 42 {
		t.Errorf("VRFTableID = %d, want 42", cfg.VRFTableID)
	}
}

func TestApplyDefaultsPropagatesGracefulRestart(t *testing.T) {
	cfg := &Config{
		GracefulRestart: &GracefulRestartConfig{Enabled: true},
	}
	cfg.ApplyDefaults()

	if cfg.GracefulRestart.RestartTime != 120 {
		t.Errorf("GracefulRestart.RestartTime = %d, want 120", cfg.GracefulRestart.RestartTime)
	}
}

func TestValidateRequiresASN(t *testing.T) {
	cfg := &Config{RouterID: "10.0.0.1"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero ASN")
	}
}

func TestValidateRequiresRouterID(t *testing.T) {
	cfg := &Config{ASN: 65000}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty RouterID")
	}
}

func TestValidateAcceptsValid(t *testing.T) {
	cfg := &Config{ASN: 65000, RouterID: "10.0.0.1", PeerMode: network.PeerModeUnnumbered, ProvisionVNI: 100, MinEstablishedPeers: 1}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRejectsNonIPv4RouterID(t *testing.T) {
	tests := []struct {
		name     string
		routerID string
	}{
		{"ipv6", "fd00::1"},
		{"hostname", "router1.example.com"},
		{"garbage", "not-an-ip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ASN: 65000, RouterID: tt.routerID}
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected error for RouterID %q", tt.routerID)
			}
		})
	}
}

func TestValidatePeerModeUnnumbered(t *testing.T) {
	cfg := &Config{
		ASN:                 65000,
		RouterID:            "10.0.0.1",
		PeerMode:            network.PeerModeUnnumbered,
		ProvisionVNI:        100,
		MinEstablishedPeers: 1,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unnumbered mode should not require neighbors: %v", err)
	}
}

func TestValidatePeerModeDualRequiresNeighbors(t *testing.T) {
	cfg := &Config{
		ASN:      65000,
		RouterID: "10.0.0.1",
		PeerMode: network.PeerModeDual,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("dual mode without neighbors should fail validation")
	}
}

func TestValidatePeerModeNumberedRequiresNeighbors(t *testing.T) {
	cfg := &Config{
		ASN:      65000,
		RouterID: "10.0.0.1",
		PeerMode: network.PeerModeNumbered,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("numbered mode without neighbors should fail validation")
	}
}

func TestValidatePeerModeDualWithNeighbors(t *testing.T) {
	cfg := &Config{
		ASN:                 65000,
		RouterID:            "10.0.0.1",
		PeerMode:            network.PeerModeDual,
		NeighborAddrs:       []string{"10.0.0.100", "10.0.0.101"},
		ProvisionVNI:        100,
		MinEstablishedPeers: 1,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("dual mode with valid neighbors should pass: %v", err)
	}
}

func TestValidatePeerModeNumberedWithNeighbors(t *testing.T) {
	cfg := &Config{
		ASN:                 65000,
		RouterID:            "10.0.0.1",
		PeerMode:            network.PeerModeNumbered,
		NeighborAddrs:       []string{"10.0.0.50"},
		RemoteASN:           65001,
		ProvisionVNI:        100,
		MinEstablishedPeers: 1,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("numbered mode with valid neighbors should pass: %v", err)
	}
}

func TestValidateRejectsInvalidNeighborAddr(t *testing.T) {
	cfg := &Config{
		ASN:           65000,
		RouterID:      "10.0.0.1",
		PeerMode:      network.PeerModeNumbered,
		NeighborAddrs: []string{"not-an-ip"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("invalid neighbor address should fail validation")
	}
}

func TestValidateRejectsUnknownPeerMode(t *testing.T) {
	cfg := &Config{
		ASN:      65000,
		RouterID: "10.0.0.1",
		PeerMode: "invalid",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("unknown peer mode should fail validation")
	}
}

func TestValidateRejectsUnsupportedOverlayType(t *testing.T) {
	cfg := &Config{
		ASN:          65000,
		RouterID:     "10.0.0.1",
		PeerMode:     network.PeerModeUnnumbered,
		ProvisionVNI: 100,
		OverlayType:  string(OverlayL3VPN),
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("l3vpn overlay type should fail validation")
	}
	if !strings.Contains(err.Error(), "overlay type") {
		t.Errorf("expected error to mention \"overlay type\", got: %v", err)
	}
}

func TestValidateRejectsHoldTimeLessThan3(t *testing.T) {
	cfg := &Config{ASN: 65000, RouterID: "10.0.0.1", PeerMode: network.PeerModeUnnumbered, ProvisionVNI: 100, HoldTime: 2, KeepaliveInterval: 1}
	if err := cfg.Validate(); err == nil {
		t.Error("HoldTime < 3 should fail RFC 4271 validation")
	}
}

func TestValidateAcceptsHoldTimeZero(t *testing.T) {
	cfg := &Config{ASN: 65000, RouterID: "10.0.0.1", PeerMode: network.PeerModeUnnumbered, ProvisionVNI: 100, HoldTime: 0}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("HoldTime 0 (defaulted) should pass: %v", err)
	}
}

func TestValidateRejectsKeepaliveExceedingHoldTime(t *testing.T) {
	cfg := &Config{ASN: 65000, RouterID: "10.0.0.1", PeerMode: network.PeerModeUnnumbered, ProvisionVNI: 100, HoldTime: 9, KeepaliveInterval: 4}
	if err := cfg.Validate(); err == nil {
		t.Error("KeepaliveInterval > HoldTime/3 should fail validation")
	}
}

func TestParsePeerMode(t *testing.T) {
	tests := []struct {
		input string
		want  network.PeerMode
	}{
		{"", network.PeerModeUnnumbered},
		{"unnumbered", network.PeerModeUnnumbered},
		{"UNNUMBERED", network.PeerModeUnnumbered},
		{"dual", network.PeerModeDual},
		{"Dual", network.PeerModeDual},
		{"numbered", network.PeerModeNumbered},
		{"NUMBERED", network.PeerModeNumbered},
		{"unknown", network.PeerModeUnnumbered},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := network.ParsePeerMode(tt.input)
			if got != tt.want {
				t.Errorf("ParsePeerMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseNeighborAddrs(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"10.0.0.1", 1},
		{"10.0.0.1,10.0.0.2", 2},
		{"10.0.0.1, 10.0.0.2, 10.0.0.3", 3},
		{",,,", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseNeighborAddrs(tt.input)
			if len(got) != tt.want {
				t.Errorf("parseNeighborAddrs(%q) = %d addrs, want %d", tt.input, len(got), tt.want)
			}
		})
	}
}

func TestApplyDefaultsSetsMinEstablishedPeersToOne(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyDefaults()

	if cfg.MinEstablishedPeers != 1 {
		t.Errorf("MinEstablishedPeers = %d, want 1 (default)", cfg.MinEstablishedPeers)
	}
}

func TestApplyDefaultsPreservesNonZeroMinEstablishedPeers(t *testing.T) {
	cfg := &Config{MinEstablishedPeers: 2}
	cfg.ApplyDefaults()

	if cfg.MinEstablishedPeers != 2 {
		t.Errorf("MinEstablishedPeers = %d, want 2 (preserved)", cfg.MinEstablishedPeers)
	}
}

func TestValidateRejectsZeroMinEstablishedPeers(t *testing.T) {
	cfg := &Config{
		ASN:                 65000,
		RouterID:            "10.0.0.1",
		PeerMode:            network.PeerModeUnnumbered,
		ProvisionVNI:        100,
		MinEstablishedPeers: 0,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("MinEstablishedPeers=0 should fail validation")
	}
}

func TestValidateAcceptsMinEstablishedPeersGreaterThanOne(t *testing.T) {
	cfg := &Config{
		ASN:                 65000,
		RouterID:            "10.0.0.1",
		PeerMode:            network.PeerModeUnnumbered,
		ProvisionVNI:        100,
		MinEstablishedPeers: 2,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("MinEstablishedPeers=2 should pass validation: %v", err)
	}
}

func TestNewConfigMapsMinEstablishedPeers(t *testing.T) {
	netCfg := &network.Config{
		UnderlayIP:   "10.0.0.1",
		ASN:          65000,
		ProvisionVNI: 4000,
		BGPPeerMode:  network.PeerModeUnnumbered,
		BGPMinPeers:  2,
	}
	cfg, err := NewConfig(netCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MinEstablishedPeers != 2 {
		t.Errorf("MinEstablishedPeers = %d, want 2", cfg.MinEstablishedPeers)
	}
}

func TestNewConfigDefaultsMinEstablishedPeersToOne(t *testing.T) {
	netCfg := &network.Config{
		UnderlayIP:   "10.0.0.1",
		ASN:          65000,
		ProvisionVNI: 4000,
		BGPPeerMode:  network.PeerModeUnnumbered,
	}
	cfg, err := NewConfig(netCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MinEstablishedPeers != 1 {
		t.Errorf("MinEstablishedPeers = %d, want 1 (default when BGPMinPeers=0)", cfg.MinEstablishedPeers)
	}
}

func TestIsiBGP(t *testing.T) {
	tests := []struct {
		name      string
		asn       uint32
		remoteASN uint32
		want      bool
	}{
		{"zero remote = iBGP", 65000, 0, true},
		{"same ASN = iBGP", 65000, 65000, true},
		{"different ASN = eBGP", 65000, 65001, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ASN: tt.asn, RemoteASN: tt.remoteASN}
			if got := cfg.IsiBGP(); got != tt.want {
				t.Errorf("IsiBGP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyDefaultsNormalizesUnderlayAFAndOverlayType(t *testing.T) {
	tests := []struct {
		name        string
		underlayAF  string
		overlayType string
		wantAF      string
		wantOT      string
	}{
		{"empty defaults", "", "", "ipv4", "evpn-vxlan"},
		{"uppercase AF", "IPV4", "EVPN-VXLAN", "ipv4", "evpn-vxlan"},
		{"mixed case", "DualStack", "L3VPN", "dual-stack", "l3vpn"},
		{"already canonical", "ipv6", "none", "ipv6", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{UnderlayAF: tt.underlayAF, OverlayType: tt.overlayType}
			cfg.ApplyDefaults()
			if cfg.UnderlayAF != tt.wantAF {
				t.Errorf("UnderlayAF = %q, want %q", cfg.UnderlayAF, tt.wantAF)
			}
			if cfg.OverlayType != tt.wantOT {
				t.Errorf("OverlayType = %q, want %q", cfg.OverlayType, tt.wantOT)
			}
		})
	}
}

func TestUnderlayTierReady(t *testing.T) {
	makeUnderlay := func(minPeers int, countFn func(context.Context) int) *UnderlayTier {
		cfg := &Config{MinEstablishedPeers: minPeers}
		u := NewUnderlayTier(cfg)
		u.peerCountFn = countFn
		return u
	}

	t.Run("succeeds_when_threshold_met_immediately", func(t *testing.T) {
		u := makeUnderlay(1, func(_ context.Context) int { return 1 })
		ctx := context.Background()
		if err := u.Ready(ctx, 5*time.Second); err != nil {
			t.Fatalf("Ready() = %v, want nil", err)
		}
	})

	t.Run("succeeds_when_threshold_met_on_second_tick", func(t *testing.T) {
		calls := 0
		u := makeUnderlay(2, func(_ context.Context) int {
			calls++
			if calls >= 2 {
				return 2
			}
			return 0
		})
		ctx := context.Background()
		if err := u.Ready(ctx, 5*time.Second); err != nil {
			t.Fatalf("Ready() = %v, want nil", err)
		}
	})

	t.Run("returns_error_on_timeout", func(t *testing.T) {
		u := makeUnderlay(3, func(_ context.Context) int { return 0 })
		ctx := context.Background()
		err := u.Ready(ctx, 1100*time.Millisecond)
		if err == nil {
			t.Fatal("Ready() = nil, want timeout error")
		}
	})

	t.Run("returns_error_on_context_cancel", func(t *testing.T) {
		u := makeUnderlay(1, func(_ context.Context) int { return 0 })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := u.Ready(ctx, 5*time.Second)
		if err == nil {
			t.Fatal("Ready() = nil, want context error")
		}
	})

	t.Run("min_peers_greater_than_one", func(t *testing.T) {
		u := makeUnderlay(3, func(_ context.Context) int { return 3 })
		ctx := context.Background()
		if err := u.Ready(ctx, 5*time.Second); err != nil {
			t.Fatalf("Ready() = %v, want nil", err)
		}
	})

	t.Run("count_below_threshold_does_not_succeed", func(t *testing.T) {
		u := makeUnderlay(3, func(_ context.Context) int { return 2 })
		ctx := context.Background()
		err := u.Ready(ctx, 1100*time.Millisecond)
		if err == nil {
			t.Fatal("Ready() = nil, want timeout error when count < min")
		}
	})
}
