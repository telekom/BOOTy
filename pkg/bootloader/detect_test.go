//go:build linux

package bootloader

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectBootloader_GRUB(t *testing.T) {
	root := t.TempDir()
	b := DetectBootloader(root)
	if _, ok := b.(*GRUB); !ok {
		t.Errorf("expected GRUB, got %T", b)
	}
}

func TestDetectBootloader_SystemdBoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "boot", "efi", "EFI", "systemd")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "systemd-bootx64.efi"
	if runtime.GOARCH == "arm64" {
		name = "systemd-bootaa64.efi"
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := DetectBootloader(root)
	if _, ok := b.(*SystemdBoot); !ok {
		t.Errorf("expected SystemdBoot, got %T", b)
	}
}
