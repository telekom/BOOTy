package secureboot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyChain_AllPresent(t *testing.T) {
	root := t.TempDir()

	// Create valid PE files (MZ header).
	mzHeader := []byte{'M', 'Z', 0, 0}
	createFile(t, filepath.Join(root, "EFI/BOOT/BOOTX64.EFI"), mzHeader)
	createFile(t, filepath.Join(root, "EFI/ubuntu/grubx64.efi"), mzHeader)
	createFile(t, filepath.Join(root, "boot/vmlinuz"), mzHeader)

	v := NewChainVerifier(nil)
	result := v.VerifyChain(root)

	if !result.Valid {
		t.Error("expected valid chain")
	}
	if !result.Shim.Signed {
		t.Error("shim should be signed")
	}
	if !result.GRUB.Signed {
		t.Error("GRUB should be signed")
	}
	if !result.Kernel.Signed {
		t.Error("kernel should be signed")
	}
}

func TestVerifyChain_MissingShim(t *testing.T) {
	root := t.TempDir()
	mzHeader := []byte{'M', 'Z', 0, 0}
	createFile(t, filepath.Join(root, "EFI/ubuntu/grubx64.efi"), mzHeader)
	createFile(t, filepath.Join(root, "boot/vmlinuz"), mzHeader)

	v := NewChainVerifier(nil)
	result := v.VerifyChain(root)

	if result.Valid {
		t.Error("expected invalid chain (missing shim)")
	}
	if result.Shim.Valid {
		t.Error("shim should be invalid")
	}
}

func TestVerifyChain_UnsignedKernel(t *testing.T) {
	root := t.TempDir()
	mzHeader := []byte{'M', 'Z', 0, 0}
	createFile(t, filepath.Join(root, "EFI/BOOT/BOOTX64.EFI"), mzHeader)
	createFile(t, filepath.Join(root, "EFI/ubuntu/grubx64.efi"), mzHeader)
	// Kernel without MZ header.
	createFile(t, filepath.Join(root, "boot/vmlinuz"), []byte("not-a-pe"))

	v := NewChainVerifier(nil)
	result := v.VerifyChain(root)

	if result.Valid {
		t.Error("expected invalid chain (unsigned kernel)")
	}
	if result.Kernel.Signed {
		t.Error("kernel should not be signed")
	}
}

func TestVerifyChain_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	v := NewChainVerifier(nil)
	result := v.VerifyChain(root)

	if result.Valid {
		t.Error("expected invalid chain (empty root)")
	}
}

func TestHasPEHeader_ValidMZ(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.efi")
	if err := os.WriteFile(path, []byte{'M', 'Z', 0, 0, 0}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	signed, signer := hasPEHeader(path)
	if !signed {
		t.Error("expected signed")
	}
	if signer != "pe-detected" {
		t.Errorf("signer = %q", signer)
	}
}

func TestHasPEHeader_InvalidHeader(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.bin")
	if err := os.WriteFile(path, []byte("not-pe"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	signed, _ := hasPEHeader(path)
	if signed {
		t.Error("expected not signed")
	}
}

func TestHasPEHeader_Missing(t *testing.T) {
	signed, _ := hasPEHeader("/nonexistent")
	if signed {
		t.Error("expected not signed for missing file")
	}
}

func TestFindFirstKernel_Glob(t *testing.T) {
	root := t.TempDir()
	bootDir := filepath.Join(root, "boot")
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bootDir, "vmlinuz-5.15.0-generic"), []byte("k"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := findFirstKernel(root)
	if got == "" {
		t.Error("expected to find kernel via glob")
	}
}

func createFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
