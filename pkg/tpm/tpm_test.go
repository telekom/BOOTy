package tpm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPCRDescription(t *testing.T) {
	tests := []struct {
		pcr  int
		want string
	}{
		{PCRFirmware, "platform firmware"},
		{PCRSecureBoot, "secure boot policy"},
		{PCRBinary, "BOOTy binary"},
		{PCRImage, "OS image checksum"},
		{PCRConfig, "provisioning config"},
		{PCRProvisioner, "provisioner identity"},
		{PCROSImage, "OS image streaming"},
		{99, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := PCRDescription(tt.pcr); got != tt.want {
				t.Errorf("PCRDescription(%d) = %q, want %q", tt.pcr, got, tt.want)
			}
		})
	}
}

func TestDetect_NoTPM(t *testing.T) {
	info := Detect()
	if info == nil {
		t.Fatal("Detect() returned nil")
	}
	_, sysfsErr := os.Stat("/sys/class/tpm/tpm0")
	sysfsExists := sysfsErr == nil
	if !sysfsExists {
		// No sysfs TPM → Present must be false and Version empty.
		if info.Present {
			t.Error("no sysfs TPM but Detect() reports Present=true")
		}
		if info.Version != "" {
			t.Errorf("no sysfs TPM but Version = %q", info.Version)
		}
		return
	}
	// If sysfs TPM exists, Present should be true.
	if !info.Present {
		t.Error("sysfs TPM exists but Detect() reports Present=false")
	}
}

func TestReadSysfsPCRs_Empty(t *testing.T) {
	result := readSysfsPCRs("/nonexistent/path")
	if result != nil {
		t.Errorf("expected nil for nonexistent path, got %v", result)
	}
}

func TestReadSysfsPCRs_ValidDir(t *testing.T) {
	dir := t.TempDir()
	pcrDir := filepath.Join(dir, "pcr-sha256")
	if err := os.MkdirAll(pcrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a valid hex digest for PCR 0
	digest := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	if err := os.WriteFile(filepath.Join(pcrDir, "0"), []byte(digest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := readSysfsPCRs(dir)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result[0]; !ok {
		t.Error("expected PCR 0 in result")
	}
}
