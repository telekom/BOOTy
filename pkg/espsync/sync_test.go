//go:build linux

package espsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyTreeMirrorsNestedFilesAndDeletesStaleEntries(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "EFI", "Vendor", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "EFI", "Vendor", "nested", "loader.efi"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "stale"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "EFI", "Vendor", "nested", "loader.efi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(destination, "stale")); !os.IsNotExist(err) {
		t.Fatalf("stale path remains: %v", err)
	}
}

func TestCopyTreeRejectsSameDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := CopyTree(dir, dir); err == nil {
		t.Fatal("expected identity rejection")
	}
}

func TestCopyTreePreservesSymlinks(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "loader"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loader", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(destination, "link"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "loader" {
		t.Fatalf("link target %q", target)
	}
}

func TestCopyTreeReplacesExistingLoaderWithoutDeletingItFirst(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "EFI", "BOOT"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "EFI", "BOOT", "bootx64.efi"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destination, "EFI", "BOOT"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "EFI", "BOOT", "bootx64.efi"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "EFI", "BOOT", "bootx64.efi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteMarkerUsesAtomicRename(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "run", "booty-esp-sync.ready")
	if err := WriteMarker(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTreeReplacesDestinationDirectoryWithFile(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "EFI", "BOOT"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "EFI", "BOOT", "loader"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destination, "EFI", "BOOT", "loader"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "EFI", "BOOT", "loader"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "file" {
		t.Fatalf("got %q", got)
	}
}

func TestCopyTreeReplacesDestinationDirectoryWithSymlink(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "EFI"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(source, "EFI", "loader")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destination, "EFI", "loader"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(destination, "EFI", "loader"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "target" {
		t.Fatalf("got %q", got)
	}
}
