//go:build linux

package provision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEfiLoaderNames(t *testing.T) {
	tests := []struct {
		arch     string
		wantShim string
		wantGrub string
	}{
		{"amd64", "shimx64.efi", "grubx64.efi"},
		{"arm64", "shimaa64.efi", "grubaa64.efi"},
		{"", "shimx64.efi", "grubx64.efi"},
	}

	for _, tc := range tests {
		t.Run(tc.arch, func(t *testing.T) {
			shim, grub := efiLoaderNames(tc.arch)
			if shim != tc.wantShim {
				t.Errorf("shim = %q, want %q", shim, tc.wantShim)
			}
			if grub != tc.wantGrub {
				t.Errorf("grub = %q, want %q", grub, tc.wantGrub)
			}
		})
	}
}

func TestEfiLoaderPath(t *testing.T) {
	root := t.TempDir()
	efiDir := filepath.Join(root, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No shim -> falls back to grub.
	loader, err := efiLoaderPath(root, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	wantGrub := "\\EFI\\ubuntu\\grubx64.efi"
	if loader != wantGrub {
		t.Errorf("got %q, want grub fallback %q", loader, wantGrub)
	}

	// Create shim -> should prefer shim.
	if err := os.WriteFile(filepath.Join(efiDir, "shimx64.efi"), []byte("shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader, err = efiLoaderPath(root, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	wantShim := "\\EFI\\ubuntu\\shimx64.efi"
	if loader != wantShim {
		t.Errorf("got %q, want shim %q", loader, wantShim)
	}

	// ARM64 without shim -> grub fallback.
	loader, err = efiLoaderPath(root, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	wantArm := "\\EFI\\ubuntu\\grubaa64.efi"
	if loader != wantArm {
		t.Errorf("got %q, want arm64 grub fallback %q", loader, wantArm)
	}
}
