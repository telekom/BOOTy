//go:build linux

package drivers

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestListLoaded(t *testing.T) {
	dir := t.TempDir()
	procPath := filepath.Join(dir, "modules")
	data := "e1000e 282624 0 - Live 0xffa00\nmlx5_core 1048576 1 mlx5_ib, Live 0xffa01\nnvme 45056 3 - Live 0xffa02\n"
	if err := os.WriteFile(procPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{log: slog.Default(), modulesDir: "/lib/modules", procModulesPath: procPath}
	mods, err := mgr.ListLoaded()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 3 {
		t.Fatalf("got %d modules, want 3", len(mods))
	}
	if mods[0].Name != "e1000e" {
		t.Errorf("mods[0].Name = %q, want %q", mods[0].Name, "e1000e")
	}
	if !mods[0].Loaded {
		t.Error("mods[0].Loaded = false, want true")
	}
	// Verify trailing comma is trimmed from dependencies.
	if len(mods[1].Dependencies) != 1 || mods[1].Dependencies[0] != "mlx5_ib" {
		t.Errorf("mods[1].Dependencies = %v, want [mlx5_ib]", mods[1].Dependencies)
	}
	if len(mods[1].UsedBy) != 1 || mods[1].UsedBy[0] != "mlx5_ib" {
		t.Errorf("mods[1].UsedBy = %v, want [mlx5_ib]", mods[1].UsedBy)
	}
}

func TestListLoaded_MissingFile(t *testing.T) {
	mgr := &Manager{log: slog.Default(), procModulesPath: "/nonexistent/proc/modules"}
	_, err := mgr.ListLoaded()
	if err == nil {
		t.Error("expected error for missing proc modules file")
	}
}

func TestFindModule(t *testing.T) {
	dir := t.TempDir()
	modDir := filepath.Join(dir, "5.15.0", "kernel", "drivers", "net")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modPath := filepath.Join(modDir, "e1000e.ko.xz")
	if err := os.WriteFile(modPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{log: slog.Default(), modulesDir: dir}
	found, err := mgr.FindModule("e1000e")
	if err != nil {
		t.Fatal(err)
	}
	if found != modPath {
		t.Errorf("FindModule = %q, want %q", found, modPath)
	}
}

func TestFindModule_NotFound(t *testing.T) {
	mgr := &Manager{log: slog.Default(), modulesDir: t.TempDir()}
	_, err := mgr.FindModule("nonexistent")
	if err == nil {
		t.Error("expected error for missing module")
	}
}

func TestIsLoaded(t *testing.T) {
	dir := t.TempDir()
	procPath := filepath.Join(dir, "modules")
	data := "e1000_e 282624 0 - Live 0xffa00\nnvme 45056 3 - Live 0xffa02\n"
	if err := os.WriteFile(procPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{log: slog.Default(), procModulesPath: procPath}
	if !mgr.IsLoaded("e1000_e") {
		t.Error("expected e1000_e to be loaded")
	}
	if mgr.IsLoaded("nonexistent") {
		t.Error("expected nonexistent to not be loaded")
	}
	// Test dash-to-underscore normalization
	if !mgr.IsLoaded("e1000-e") {
		t.Error("expected e1000-e to match loaded e1000_e")
	}
}

func TestIsLoaded_MissingFile(t *testing.T) {
	mgr := &Manager{log: slog.Default(), procModulesPath: "/nonexistent"}
	if mgr.IsLoaded("anything") {
		t.Error("expected false when proc file missing")
	}
}

func TestNewManager_NilLogger(t *testing.T) {
	mgr := NewManager(nil)
	if mgr.log == nil {
		t.Error("expected default logger, got nil")
	}
}

func TestManifest_AllModules(t *testing.T) {
	m := &Manifest{
		Common:  []string{"vxlan", "bridge", ""},
		NICs:    []string{"e1000e", "mlx5_core"},
		Storage: []string{"nvme", "nvme_core"},
		USB:     []string{"xhci_hcd"},
		Custom:  []string{"vxlan"}, // duplicate should be deduped
	}

	got := m.AllModules()
	if len(got) != 7 {
		t.Errorf("AllModules count = %d, want 7 (got: %v)", len(got), got)
	}

	if got[0] != "vxlan" {
		t.Errorf("first module = %q, want vxlan", got[0])
	}
	for _, mod := range got {
		if mod == "" {
			t.Error("AllModules should not include empty module names")
		}
	}
}

func TestManifest_AllModules_Empty(t *testing.T) {
	m := &Manifest{}
	got := m.AllModules()
	if len(got) != 0 {
		t.Errorf("AllModules empty = %d, want 0", len(got))
	}
}
