package tpm

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_WithMockSysfs(t *testing.T) {
	// Create mock sysfs structure in temp dir.
	tmpDir := t.TempDir()
	tpmDir := filepath.Join(tmpDir, "tpm0")
	if err := os.MkdirAll(tpmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpmDir, "tpm_version_major"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	deviceDir := filepath.Join(tpmDir, "device")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deviceDir, "vendor"), []byte("0x1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	fwDir := filepath.Join(deviceDir, "firmware_node")
	if err := os.MkdirAll(fwDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fwDir, "description"), []byte("TPM 2.0"), 0o644); err != nil {
		t.Fatal(err)
	}

	// We can't override sysfsTPMBase (const), so test helper functions directly.
	v, err := readSysfsFile(filepath.Join(tpmDir, "tpm_version_major"))
	if err != nil {
		t.Fatalf("readSysfsFile: %v", err)
	}
	if v != "2" {
		t.Errorf("version = %q, want %q", v, "2")
	}

	m, err := readSysfsFile(filepath.Join(deviceDir, "vendor"))
	if err != nil {
		t.Fatalf("readSysfsFile vendor: %v", err)
	}
	if m != "0x1234" {
		t.Errorf("manufacturer = %q", m)
	}
}

func TestReadSysfsFile_NotExist(t *testing.T) {
	_, err := readSysfsFile("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestFileExists(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test")
	if fileExists(tmpFile) {
		t.Error("should not exist before creation")
	}
	if err := os.WriteFile(tmpFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(tmpFile) {
		t.Error("should exist after creation")
	}
}

func TestReadSysfsPCRs_ValidData(t *testing.T) {
	tmpDir := t.TempDir()
	pcrDir := filepath.Join(tmpDir, "pcr-sha256")
	if err := os.MkdirAll(pcrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a valid PCR value.
	hexVal := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(filepath.Join(pcrDir, "0"), []byte(hexVal), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write an invalid PCR filename.
	if err := os.WriteFile(filepath.Join(pcrDir, "notanumber"), []byte(hexVal), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write invalid hex content.
	if err := os.WriteFile(filepath.Join(pcrDir, "1"), []byte("not-hex"), 0o644); err != nil {
		t.Fatal(err)
	}

	pcrs := readSysfsPCRs(tmpDir)
	if len(pcrs) != 1 {
		t.Errorf("expected 1 valid PCR, got %d", len(pcrs))
	}
	if _, ok := pcrs[0]; !ok {
		t.Error("expected PCR 0 to be present")
	}
}
