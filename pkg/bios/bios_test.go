package bios_test

import (
	"context"
	"errors"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"

	// Blank imports trigger vendor init() registration.
	_ "github.com/telekom/BOOTy/pkg/bios/dell"
	_ "github.com/telekom/BOOTy/pkg/bios/hpe"
	_ "github.com/telekom/BOOTy/pkg/bios/lenovo"
	_ "github.com/telekom/BOOTy/pkg/bios/supermicro"
)

func TestVendorManagers(t *testing.T) {
	vendors := []system.Vendor{
		system.VendorHPE,
		system.VendorDell,
		system.VendorLenovo,
		system.VendorSupermicro,
	}
	for _, vendor := range vendors {
		t.Run(string(vendor), func(t *testing.T) {
			mgr, err := bios.NewManager(vendor, nil)
			if err != nil {
				t.Fatalf("NewManager(%s): %v", vendor, err)
			}
			if mgr.Vendor() != vendor {
				t.Errorf("vendor = %q, want %q", mgr.Vendor(), vendor)
			}
			state, err := mgr.Capture(context.Background())
			if err != nil {
				t.Fatalf("Capture: %v", err)
			}
			if state.Vendor != vendor {
				t.Errorf("state vendor = %q", state.Vendor)
			}
			changes := []bios.SettingChange{{Name: "Test", Value: "Value"}}
			_, err = mgr.Apply(context.Background(), changes)
			if !errors.Is(err, bios.ErrNotImplemented) {
				t.Errorf("Apply error = %v, want ErrNotImplemented", err)
			}
			err = mgr.Reset(context.Background())
			if !errors.Is(err, bios.ErrNotImplemented) {
				t.Errorf("Reset error = %v, want ErrNotImplemented", err)
			}
		})
	}
}

func TestCompare_Match(t *testing.T) {
	baseline := &bios.Baseline{
		Settings: map[string]string{"BootMode": "Uefi", "SecureBoot": "Enabled"},
	}
	state := &bios.State{
		Settings: map[string]bios.Setting{
			"BootMode":   {CurrentValue: "Uefi"},
			"SecureBoot": {CurrentValue: "Enabled"},
		},
	}
	diff := bios.Compare(baseline, state)
	if !diff.Matches {
		t.Error("expected match")
	}
}

func TestCompare_Mismatch(t *testing.T) {
	baseline := &bios.Baseline{
		Settings: map[string]string{"BootMode": "Uefi"},
	}
	state := &bios.State{
		Settings: map[string]bios.Setting{"BootMode": {CurrentValue: "Legacy"}},
	}
	diff := bios.Compare(baseline, state)
	if diff.Matches {
		t.Error("expected mismatch")
	}
}

func TestCompare_BothNil(t *testing.T) {
	diff := bios.Compare(nil, nil)
	if !diff.Matches {
		t.Error("both nil should match")
	}
}

func TestNewManager_Unregistered(t *testing.T) {
	_, err := bios.NewManager("unknown-vendor", nil)
	if err == nil {
		t.Error("expected error for unregistered vendor")
	}
}

func TestBaseManager_NilSettings(t *testing.T) {
	mgr := bios.NewBaseManager(system.VendorSupermicro, nil, nil)
	state, err := mgr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(state.Settings) != 0 {
		t.Errorf("expected 0 settings, got %d", len(state.Settings))
	}
}
