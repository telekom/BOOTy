package lenovo

import (
	"context"
	"log/slog"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

func TestLenovoManagerRegistered(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorLenovo, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(Lenovo): %v", err)
	}
	if mgr.Vendor() != system.VendorLenovo {
		t.Errorf("vendor = %q, want %q", mgr.Vendor(), system.VendorLenovo)
	}
}

func TestLenovoCriticalSettings(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorLenovo, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(Lenovo): %v", err)
	}
	state, err := mgr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	expected := map[string]string{
		"OperatingMode":            "MaximumPerformance",
		"HyperThreading":           "Enable",
		"VirtualizationTechnology": "Enable",
		"SRIOVSupport":             "Enable",
		"BootMode":                 "UEFIMode",
		"SecureBoot":               "Enable",
		"TurboMode":                "Enable",
		"IntelSpeedStep":           "Enable",
		"ActiveProcessorCores":     "All",
		"PackageCState":            "C0/C1",
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
