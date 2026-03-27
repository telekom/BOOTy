package hpe

import (
	"context"
	"log/slog"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

func TestHPEManagerRegistered(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorHPE, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(HPE): %v", err)
	}
	if mgr.Vendor() != system.VendorHPE {
		t.Errorf("vendor = %q, want %q", mgr.Vendor(), system.VendorHPE)
	}
}

func TestHPECriticalSettings(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorHPE, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(HPE): %v", err)
	}
	state, err := mgr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	expected := map[string]string{
		"ProcHyperthreading":      "Enabled",
		"ProcVirtualization":      "Enabled",
		"Sriov":                   "Enabled",
		"BootMode":                "Uefi",
		"SecureBootStatus":        "Enabled",
		"WorkloadProfile":         "GeneralPowerEfficientCompute",
		"PowerRegulator":          "DynamicPowerSavings",
		"ThermalConfig":           "OptimalCooling",
		"IntelligentProvisioning": "Disabled",
		"EmbSata1Aspm":            "Disabled",
	}
	for name, want := range expected {
		s, ok := state.Settings[name]
		if !ok {
			t.Errorf("missing setting %q", name)
			continue
		}
		if s.CurrentValue != want {
			t.Errorf("setting %q = %q, want %q", name, s.CurrentValue, want)
		}
	}
	if len(state.Settings) != len(expected) {
		t.Errorf("settings count = %d, want %d", len(state.Settings), len(expected))
	}
}
