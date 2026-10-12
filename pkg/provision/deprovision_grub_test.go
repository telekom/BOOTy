//go:build linux

package provision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisableGRUBConfig(t *testing.T) {
	write := func(t *testing.T, root, rel string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("menuentry"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("debian path renamed", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "boot/grub/grub.cfg")
		if err := disableGRUBConfig(root); err != nil {
			t.Fatalf("disableGRUBConfig() = %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "boot/grub/grub.cfg.bak")); err != nil {
			t.Fatalf("grub.cfg.bak missing: %v", err)
		}
	})
	t.Run("rhel path renamed", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "boot/grub2/grub.cfg")
		if err := disableGRUBConfig(root); err != nil {
			t.Fatalf("disableGRUBConfig() = %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "boot/grub2/grub.cfg")); !os.IsNotExist(err) {
			t.Fatal("grub2/grub.cfg still present")
		}
	})
	t.Run("rerun after rename succeeds", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "boot/grub/grub.cfg.bak")
		if err := disableGRUBConfig(root); err != nil {
			t.Fatalf("disableGRUBConfig() = %v", err)
		}
	})
	t.Run("missing config fails", func(t *testing.T) {
		if err := disableGRUBConfig(t.TempDir()); err == nil {
			t.Fatal("disableGRUBConfig() = nil, want error when no grub.cfg exists")
		}
	})
}
