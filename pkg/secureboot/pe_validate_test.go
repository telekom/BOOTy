package secureboot

import (
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// peOptionalHeader32PlusMinSize is the minimum size of a PE32+ optional header
// (IMAGE_OPTIONAL_HEADER64 = 112 bytes), as defined in the PE/COFF specification
// §3.4. Note: PE32 (32-bit) uses IMAGE_OPTIONAL_HEADER32 (96 bytes); this
// constant targets 64-bit EFI binaries which always use the 112-byte variant.
const peOptionalHeader32PlusMinSize = 112

// hostMachineType returns the PE machine type constant matching the host arch.
// Tests use this to build minimal PE binaries that pass the arch validation added
// to validatePEHeader.
func hostMachineType() uint16 {
	switch runtime.GOARCH {
	case "arm64":
		return pe.IMAGE_FILE_MACHINE_ARM64
	default:
		return pe.IMAGE_FILE_MACHINE_AMD64
	}
}

// minimalValidPE returns a minimal PE32+ (64-bit) binary whose machine type
// matches the host architecture, for use in tests that exercise validatePEHeader.
// PE32+ uses magic 0x020b; the machine field is set from hostMachineType().
func minimalValidPE() []byte {
	const (
		dosStubSize   = 64
		peSignature   = 4
		coffHdrSize   = 20
		optHdrOffset  = dosStubSize + peSignature + coffHdrSize
		magicPE32Plus = uint16(0x020b)
	)
	machine := hostMachineType()
	buf := make([]byte, optHdrOffset+peOptionalHeader32PlusMinSize)

	buf[0] = 'M'
	buf[1] = 'Z'
	binary.LittleEndian.PutUint32(buf[0x3c:], dosStubSize)

	copy(buf[dosStubSize:], []byte("PE\x00\x00"))

	coffBase := dosStubSize + peSignature
	binary.LittleEndian.PutUint16(buf[coffBase:], machine)
	binary.LittleEndian.PutUint16(buf[coffBase+16:], peOptionalHeader32PlusMinSize)
	binary.LittleEndian.PutUint16(buf[coffBase+18:], 0x0002)

	binary.LittleEndian.PutUint16(buf[optHdrOffset:], magicPE32Plus)

	return buf
}

func writeTempFile(t *testing.T, data []byte, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}
	return path
}

func TestValidatePEHeader_ValidPE(t *testing.T) {
	path := writeTempFile(t, minimalValidPE(), "boot.efi")
	if err := validatePEHeader(path); err != nil {
		t.Errorf("expected valid PE to pass, got: %v", err)
	}
}

func TestValidatePEHeader_TruncatedMZ(t *testing.T) {
	// MZ signature only — no PE signature
	data := []byte{'M', 'Z', 0x00, 0x00}
	path := writeTempFile(t, data, "truncated.efi")
	if err := validatePEHeader(path); err == nil {
		t.Error("expected error for truncated MZ-only file, got nil")
	}
}

func TestValidatePEHeader_NonPE(t *testing.T) {
	data := []byte("this is plain text, not a PE binary at all")
	path := writeTempFile(t, data, "notpe.efi")
	if err := validatePEHeader(path); err == nil {
		t.Error("expected error for non-PE file, got nil")
	}
}

func TestValidatePEHeader_EmptyFile(t *testing.T) {
	path := writeTempFile(t, []byte{}, "empty.efi")
	if err := validatePEHeader(path); err == nil {
		t.Error("expected error for empty file, got nil")
	}
}

func TestFindValidCandidate_SkipsInvalidPEThenFindsValid(t *testing.T) {
	dir := t.TempDir()

	invalid := filepath.Join(dir, "bad.efi")
	if err := os.WriteFile(invalid, []byte("not a PE binary"), 0o600); err != nil {
		t.Fatalf("write invalid: %v", err)
	}

	valid := filepath.Join(dir, "good.efi")
	if err := os.WriteFile(valid, minimalValidPE(), 0o600); err != nil {
		t.Fatalf("write valid: %v", err)
	}

	status := findValidCandidate("shim", []string{invalid, valid})
	if status.Error != "" {
		t.Errorf("expected valid candidate to be found, got error: %s", status.Error)
	}
}

func TestFindValidCandidate_AllInvalidPEReturnsError(t *testing.T) {
	dir := t.TempDir()

	bad1 := filepath.Join(dir, "bad1.efi")
	bad2 := filepath.Join(dir, "bad2.efi")
	for _, p := range []string{bad1, bad2} {
		if err := os.WriteFile(p, []byte("garbage"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	status := findValidCandidate("shim", []string{bad1, bad2})
	if status.Error == "" {
		t.Error("expected error when all candidates have invalid PE headers")
	}
	if !strings.HasPrefix(status.Error, "pe/coff validation failed for all candidates") {
		t.Errorf("expected pe/coff validation failure prefix, got misleading message: %q", status.Error)
	}
}

func TestIsEFIPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/boot/efi/EFI/BOOT/BOOTX64.EFI", true},
		{"/boot/efi/EFI/ubuntu/grubx64.efi", true},
		{"/boot/vmlinuz", false},
		{"/boot/vmlinuz-linux", false},
		{"/boot/efi/EFI/BOOT/BOOTAA64.EFI", true},
	}
	for _, tc := range cases {
		got := isEFIPath(tc.path)
		if got != tc.want {
			t.Errorf("isEFIPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
