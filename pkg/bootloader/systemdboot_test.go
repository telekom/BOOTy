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
		DefaultKernel: "ubuntu",
		Timeout:       5,
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

	os.WriteFile(filepath.Join(entriesDir, "ubuntu.conf"), []byte(entry1), 0o644)
	os.WriteFile(filepath.Join(entriesDir, "recovery.conf"), []byte(entry2), 0o644)

	sb := &SystemdBoot{Log: slog.Default()}
	entries, err := sb.ListEntries(context.Background(), root)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}

func TestParseLoaderEntry(t *testing.T) {
	dir := t.TempDir()
	content := "title   Test Entry\nlinux   /vmlinuz\ninitrd  /initrd.img\noptions root=UUID=test ro\n"
	path := filepath.Join(dir, "test.conf")
	os.WriteFile(path, []byte(content), 0o644)

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
