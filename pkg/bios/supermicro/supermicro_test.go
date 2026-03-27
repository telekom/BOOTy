package supermicro

import (
	"context"
	"log/slog"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

func TestSupermicroManagerRegistered(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorSupermicro, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(Supermicro): %v", err)
	}
	if mgr.Vendor() != system.VendorSupermicro {
		t.Errorf("vendor = %q, want %q", mgr.Vendor(), system.VendorSupermicro)
	}
}

func TestSupermicroNilSettings(t *testing.T) {
	mgr, err := bios.NewManager(system.VendorSupermicro, slog.Default())
	if err != nil {
		t.Fatalf("NewManager(Supermicro): %v", err)
	}
	state, err := mgr.Capture(context.Background())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(state.Settings) != 0 {
		t.Errorf("settings count = %d, want 0", len(state.Settings))
	}
}
