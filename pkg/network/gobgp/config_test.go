package gobgp

import (
	"testing"
)

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
	cfg := &Config{ASN: 65000, RouterID: "10.0.0.1"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
