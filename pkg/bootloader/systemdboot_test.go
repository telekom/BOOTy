//go:build linux

package bootloader

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdBoot_Configure(t *testing.T) {
	espDir := t.TempDir()

	sb := &SystemdBoot{
		Log:     slog.Default(),
		espPath: espDir,
	}

	cfg := BootConfig{
		DefaultEntry: "ubuntu",
		Timeout:      5,
		Entries: []BootEntry{
			{
				ID:      "ubuntu",
				Title:   "Ubuntu 22.04",
				Kernel:  "/vmlinuz-5.15.0",
				Initrd:  "/initrd.img-5.15.0",
				Cmdline: "root=UUID=abc-123 ro quiet",
			},
		},
	}

	if err := sb.Configure(context.Background(), &cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	// Check loader.conf
	loaderData, err := os.ReadFile(filepath.Join(espDir, "loader", "loader.conf"))
	if err != nil {
		t.Fatalf("read loader.conf: %v", err)
	}
	loaderStr := string(loaderData)
	if !strings.Contains(loaderStr, "default ubuntu.conf") {
		t.Errorf("loader.conf missing default: %s", loaderStr)
	}
	if !strings.Contains(loaderStr, "timeout 5") {
		t.Errorf("loader.conf missing timeout: %s", loaderStr)
	}

	// Check entry file
	entryData, err := os.ReadFile(filepath.Join(espDir, "loader", "entries", "ubuntu.conf"))
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	entryStr := string(entryData)
	if !strings.Contains(entryStr, "title   Ubuntu 22.04") {
		t.Errorf("entry missing title: %s", entryStr)
	}
	if !strings.Contains(entryStr, "linux   /vmlinuz-5.15.0") {
		t.Errorf("entry missing kernel: %s", entryStr)
	}
}

func TestSystemdBoot_ListEntries(t *testing.T) {
	root := t.TempDir()
	entriesDir := filepath.Join(root, "boot", "efi", "loader", "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	entry1 := "title   Ubuntu 22.04\nlinux   /vmlinuz-5.15.0\ninitrd  /initrd.img-5.15.0\noptions root=UUID=abc ro\n"
	entry2 := "title   Recovery\nlinux   /vmlinuz-5.15.0\ninitrd  /initrd.img-5.15.0\noptions root=UUID=abc ro single\n"

	if err := os.WriteFile(filepath.Join(entriesDir, "ubuntu.conf"), []byte(entry1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entriesDir, "recovery.conf"), []byte(entry2), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := &SystemdBoot{Log: slog.Default()}
	entries, err := sb.ListEntries(context.Background(), root)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}

func TestSystemdBoot_ListEntriesUsesESPPath(t *testing.T) {
	root := t.TempDir()
	esp := t.TempDir()
	entriesDir := filepath.Join(esp, "loader", "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := "title   From ESP\nlinux   /vmlinuz-esp\noptions root=UUID=esp ro\n"
	if err := os.WriteFile(filepath.Join(entriesDir, "esp.conf"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := &SystemdBoot{Log: slog.Default(), espPath: esp}
	entries, err := sb.ListEntries(context.Background(), root)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].ID != "esp" || entries[0].Kernel != "/vmlinuz-esp" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestParseLoaderEntry(t *testing.T) {
	dir := t.TempDir()
	content := "title   Test Entry\nlinux   /vmlinuz\ninitrd  /initrd.img\noptions root=UUID=test ro\n"
	path := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := parseLoaderEntry(path)
	if err != nil {
		t.Fatalf("parseLoaderEntry: %v", err)
	}
	if entry.ID != "test" {
		t.Errorf("ID = %q, want %q", entry.ID, "test")
	}
	if entry.Title != "Test Entry" {
		t.Errorf("Title = %q, want %q", entry.Title, "Test Entry")
	}
	if entry.Kernel != "/vmlinuz" {
		t.Errorf("Kernel = %q", entry.Kernel)
	}
	if entry.Initrd != "/initrd.img" {
		t.Errorf("Initrd = %q", entry.Initrd)
	}
}

func TestParseLoaderEntry_Missing(t *testing.T) {
	_, err := parseLoaderEntry("/nonexistent/test.conf")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestSystemdBoot_InstallFallback(t *testing.T) {
	// Force bootctl to not be found so the fallback path is always exercised.
	t.Setenv("PATH", t.TempDir())

	root := t.TempDir()
	esp := filepath.Join(root, "boot", "efi")
	if err := os.MkdirAll(esp, 0o755); err != nil {
		t.Fatal(err)
	}

	// Place a fake EFI binary in the target OS path.
	efiSrcDir := filepath.Join(root, "usr", "lib", "systemd", "boot", "efi")
	if err := os.MkdirAll(efiSrcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	efiContent := []byte("fake-efi-binary")
	efiBootBin, efiFallbackBin := efiFileNames()
	if err := os.WriteFile(filepath.Join(efiSrcDir, efiBootBin), efiContent, 0o644); err != nil {
		t.Fatal(err)
	}

	sb := NewSystemdBoot(slog.Default())
	// bootctl is not in PATH during tests, so the fallback path is exercised.
	if err := sb.Install(context.Background(), root, esp); err != nil {
		t.Fatalf("Install fallback: %v", err)
	}

	// Verify EFI files were copied.
	dst := filepath.Join(esp, "EFI", "systemd", efiBootBin)
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read installed EFI: %v", err)
	}
	if string(data) != "fake-efi-binary" {
		t.Errorf("EFI content = %q", string(data))
	}

	fallback := filepath.Join(esp, "EFI", "BOOT", efiFallbackBin)
	if _, err := os.Stat(fallback); err != nil {
		t.Errorf("%s not created: %v", efiFallbackBin, err)
	}
}

func TestSystemdBoot_SetDefaultFallback(t *testing.T) {
	// Force bootctl to not be found so the fallback path is always exercised.
	t.Setenv("PATH", t.TempDir())

	esp := t.TempDir()
	loaderDir := filepath.Join(esp, "loader")
	entriesDir := filepath.Join(loaderDir, "entries")
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an initial loader.conf with a default entry.
	initial := "timeout 5\ndefault old-entry.conf\n"
	if err := os.WriteFile(filepath.Join(loaderDir, "loader.conf"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entriesDir, "new-entry.conf"), []byte("title x\nlinux /vmlinuz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := &SystemdBoot{
		Log:     slog.Default(),
		espPath: esp,
	}

	if err := sb.SetDefault(context.Background(), "new-entry.conf"); err != nil {
		t.Fatalf("SetDefault fallback: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(loaderDir, "loader.conf"))
	if err != nil {
		t.Fatalf("read loader.conf: %v", err)
	}
	if !strings.Contains(string(data), "default new-entry.conf") {
		t.Errorf("loader.conf = %q, want default new-entry.conf", string(data))
	}
	if strings.Contains(string(data), "old-entry") {
		t.Errorf("loader.conf still has old-entry: %q", string(data))
	}
}

func TestSystemdBoot_SetDefaultRejectsInvalidIDs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	esp := t.TempDir()
	loaderDir := filepath.Join(esp, "loader")
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loaderDir, "loader.conf"), []byte("default old.conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := &SystemdBoot{Log: slog.Default(), espPath: esp}
	tests := []string{"bad id", "bad\nid", "bad\tid", "bad\\id", "../bad", ""}
	for _, id := range tests {
		id := id
		t.Run(id, func(t *testing.T) {
			if err := sb.SetDefault(context.Background(), id); err == nil {
				t.Fatalf("expected error for invalid id %q", id)
			}
		})
	}
}

func TestSystemdBoot_SetDefaultRequiresExistingEntryWhenEntriesDirExists(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	esp := t.TempDir()
	loaderDir := filepath.Join(esp, "loader")
	entriesDir := filepath.Join(loaderDir, "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loaderDir, "loader.conf"), []byte("default old.conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := &SystemdBoot{Log: slog.Default(), espPath: esp}
	if err := sb.SetDefault(context.Background(), "missing-entry"); err == nil {
		t.Fatal("expected error for missing entry file")
	}
}

func TestSystemdBoot_ConfigureNilConfig(t *testing.T) {
	sb := &SystemdBoot{Log: slog.Default(), espPath: t.TempDir()}
	if err := sb.Configure(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestSystemdBoot_GenerateLoaderConfValidation(t *testing.T) {
	sb := &SystemdBoot{Log: slog.Default(), espPath: t.TempDir()}

	if err := sb.generateLoaderConf(&BootConfig{DefaultEntry: "ubuntu", Timeout: -1}); err == nil {
		t.Fatal("expected error for negative timeout")
	}
	if err := sb.generateLoaderConf(&BootConfig{DefaultEntry: "bad entry", Timeout: 5}); err == nil {
		t.Fatal("expected error for whitespace default entry")
	}
	if err := sb.generateLoaderConf(&BootConfig{DefaultEntry: "bad\\entry", Timeout: 5}); err == nil {
		t.Fatal("expected error for path-like default entry")
	}
}

func TestSystemdBoot_GenerateEntryRejectsInvalidID(t *testing.T) {
	sb := &SystemdBoot{Log: slog.Default(), espPath: t.TempDir()}
	tests := []string{"bad id", "bad\nentry", "bad\\entry", "../entry"}
	for _, id := range tests {
		id := id
		t.Run(id, func(t *testing.T) {
			err := sb.generateEntry(&BootEntry{ID: id, Kernel: "/vmlinuz", Title: "x"})
			if err == nil {
				t.Fatalf("expected invalid id error for %q", id)
			}
		})
	}
}

func TestParseLoaderEntry_MissingKernel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.conf")
	if err := os.WriteFile(path, []byte("title Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := parseLoaderEntry(path); err == nil {
		t.Fatal("expected error for entry without linux line")
	}
}
