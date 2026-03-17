//go:build e2e && linux

package e2e

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/telekom/BOOTy/pkg/bootloader"
)

func TestSystemdBootFallbackLifecycle(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Force fallback path without bootctl.

	root := t.TempDir()
	esp := filepath.Join(root, "boot", "efi")
	if err := os.MkdirAll(esp, 0o755); err != nil {
		t.Fatalf("create esp dir: %v", err)
	}

	efiBootBin := "systemd-bootx64.efi"
	if runtime.GOARCH == "arm64" {
		efiBootBin = "systemd-bootaa64.efi"
	}
	efiDir := filepath.Join(root, "usr", "lib", "systemd", "boot", "efi")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatalf("create efi source dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(efiDir, efiBootBin), []byte("efi"), 0o644); err != nil {
		t.Fatalf("write fake efi binary: %v", err)
	}

	sb := bootloader.NewSystemdBoot(slog.Default())
	if err := sb.Install(context.Background(), root, esp); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg := &bootloader.BootConfig{
		DefaultEntry: "booty-e2e",
		Timeout:      3,
		Entries: []bootloader.BootEntry{{
			ID:      "booty-e2e",
			Title:   "BOOTy E2E",
			Kernel:  "/vmlinuz-e2e",
			Cmdline: "root=UUID=e2e ro",
		}},
	}
	if err := sb.Configure(context.Background(), cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	if err := sb.SetDefault(context.Background(), "booty-e2e.conf"); err != nil {
		t.Fatalf("set default: %v", err)
	}

	if got := bootloader.DetectBootloader(root); got != "systemd-boot" {
		t.Fatalf("detect bootloader = %q, want systemd-boot", got)
	}

	entries, err := sb.ListEntries(context.Background(), root)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "booty-e2e" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}
