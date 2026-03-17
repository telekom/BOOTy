package bootloader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectBootloader(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(root string) error
		expected string
	}{
		{
			name: "systemd-boot x64",
			setup: func(root string) error {
				p := filepath.Join(root, "usr", "lib", "systemd", "boot", "efi")
				if err := os.MkdirAll(p, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(p, "systemd-bootx64.efi"), []byte("stub"), 0o644)
			},
			expected: "systemd-boot",
		},
		{
			name: "systemd-boot arm64",
			setup: func(root string) error {
				p := filepath.Join(root, "usr", "lib", "systemd", "boot", "efi")
				if err := os.MkdirAll(p, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(p, "systemd-bootaa64.efi"), []byte("stub"), 0o644)
			},
			expected: "systemd-boot",
		},
		{
			name: "grub",
			setup: func(root string) error {
				p := filepath.Join(root, "usr", "sbin")
				if err := os.MkdirAll(p, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(p, "grub-install"), []byte("stub"), 0o755)
			},
			expected: "grub",
		},
		{
			name: "grub2 (RHEL)",
			setup: func(root string) error {
				p := filepath.Join(root, "usr", "sbin")
				if err := os.MkdirAll(p, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(p, "grub2-install"), []byte("stub"), 0o755)
			},
			expected: "grub",
		},
		{
			name: "prefer grub when both generic binaries exist",
			setup: func(root string) error {
				grubDir := filepath.Join(root, "usr", "sbin")
				if err := os.MkdirAll(grubDir, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(grubDir, "grub-install"), []byte("stub"), 0o755); err != nil {
					return err
				}
				sdDir := filepath.Join(root, "usr", "lib", "systemd", "boot", "efi")
				if err := os.MkdirAll(sdDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(sdDir, "systemd-bootx64.efi"), []byte("stub"), 0o644)
			},
			expected: "grub",
		},
		{
			name: "prefer systemd-boot artifact over grub binary",
			setup: func(root string) error {
				if err := os.MkdirAll(filepath.Join(root, "boot", "efi", "loader"), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(root, "boot", "efi", "loader", "loader.conf"), []byte("default test.conf\n"), 0o644); err != nil {
					return err
				}
				grubDir := filepath.Join(root, "usr", "sbin")
				if err := os.MkdirAll(grubDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(grubDir, "grub-install"), []byte("stub"), 0o755)
			},
			expected: "systemd-boot",
		},
		{
			name:     "unknown",
			setup:    func(_ string) error { return nil },
			expected: "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := tc.setup(root); err != nil {
				t.Fatalf("setup: %v", err)
			}
			got := DetectBootloader(root)
			if got != tc.expected {
				t.Errorf("DetectBootloader() = %q, want %q", got, tc.expected)
			}
		})
	}
}
