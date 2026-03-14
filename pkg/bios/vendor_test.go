package bios_test

import (
	"context"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/bios/dell"
	"github.com/telekom/BOOTy/pkg/bios/hpe"
	"github.com/telekom/BOOTy/pkg/bios/lenovo"
	"github.com/telekom/BOOTy/pkg/bios/supermicro"
)

func TestVendorManagers(t *testing.T) {
	managers := []bios.Manager{
		hpe.New(nil),
		dell.New(nil),
		lenovo.New(nil),
		supermicro.New(nil),
	}

	expectedVendors := []bios.Vendor{
		bios.VendorHPE,
		bios.VendorDell,
		bios.VendorLenovo,
		bios.VendorSupermicro,
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
			if err == nil {
				t.Error("expected Reset error (not implemented)")
			}
		})
	}
}

func TestHPECriticalSettings(t *testing.T) {
	if len(hpe.CriticalSettings) == 0 {
		t.Error("HPE CriticalSettings should not be empty")
	}
	if _, ok := hpe.CriticalSettings["ProcHyperthreading"]; !ok {
		t.Error("missing ProcHyperthreading")
	}
}

func TestDellCriticalSettings(t *testing.T) {
	if len(dell.CriticalSettings) == 0 {
		t.Error("Dell CriticalSettings should not be empty")
	}
	if _, ok := dell.CriticalSettings["BootMode"]; !ok {
		t.Error("missing BootMode")
	}
}

func TestLenovoCriticalSettings(t *testing.T) {
	if len(lenovo.CriticalSettings) == 0 {
		t.Error("Lenovo CriticalSettings should not be empty")
	}
	if _, ok := lenovo.CriticalSettings["HyperThreading"]; !ok {
		t.Error("missing HyperThreading")
	}
}
