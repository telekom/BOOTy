package dell

import (
	"context"
	"log/slog"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

func TestDellManagerRegistered(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorDell, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(Dell): %v", err)
	}
	if mgr.Vendor() != system.VendorDell {
		t.Errorf("vendor = %q, want %q", mgr.Vendor(), system.VendorDell)
	}
}

func TestDellCriticalSettings(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorDell, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(Dell): %v", err)
	}
	state, err := mgr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	expected := map[string]string{
		"LogicalProc":              "Enabled",
		"VirtualizationTechnology": "Enabled",
		"SriovGlobalEnable":        "Enabled",
		"BootMode":                 "Uefi",
		"SecureBoot":               "Enabled",
		"SystemProfile":            "Performance",
		"ProcTurboMode":            "Enabled",
		"ProcCStates":              "Disabled",
		"MemTest":                  "Disabled",
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
