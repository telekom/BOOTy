//go:build linux

package bootloader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectBootloader_GRUB(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	b := DetectBootloader(root)
	if _, ok := b.(*GRUB); !ok {
		t.Errorf("expected GRUB, got %T", b)
	}
}

func TestDetectBootloader_SystemdBoot(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "boot", "efi", "EFI", "systemd")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "systemd-bootx64.efi"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := DetectBootloader(root)
	if _, ok := b.(*SystemdBoot); !ok {
		t.Errorf("expected SystemdBoot, got %T", b)
	}
}
