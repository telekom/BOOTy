package bios_test

import (
	"context"
	"errors"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/bios/dell"
	"github.com/telekom/BOOTy/pkg/bios/hpe"
	"github.com/telekom/BOOTy/pkg/bios/lenovo"
	"github.com/telekom/BOOTy/pkg/bios/supermicro"
	"github.com/telekom/BOOTy/pkg/system"
)

func TestVendorManagers(t *testing.T) {
	managers := []bios.Manager{
		hpe.New(nil),
		dell.New(nil),
		lenovo.New(nil),
		supermicro.New(nil),
	}
	expectedVendors := []system.Vendor{
		system.VendorHPE,
		system.VendorDell,
		system.VendorLenovo,
		system.VendorSupermicro,
	}
	for i, mgr := range managers {
		t.Run(string(expectedVendors[i]), func(t *testing.T) {
			if mgr.Vendor() != expectedVendors[i] {
				t.Errorf("vendor = %q, want %q", mgr.Vendor(), expectedVendors[i])
			}
			state, err := mgr.Capture(context.Background())
			if err != nil {
				t.Fatalf("Capture: %v", err)
			}
			if state.Vendor != expectedVendors[i] {
				t.Errorf("state vendor = %q", state.Vendor)
			}
			changes := []bios.SettingChange{{Name: "Test", Value: "Value"}}
			reboot, err := mgr.Apply(context.Background(), changes)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(reboot) != 1 {
				t.Errorf("reboot = %d", len(reboot))
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
