//go:build linux

package tpm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectWithFixture(t *testing.T) {
	// Save and restore original paths.
	origDev, origRM, origSys := tpmDevicePath, tpmrmDevicePath, sysTPMPath
	defer func() {
		tpmDevicePath = origDev
		tpmrmDevicePath = origRM
		sysTPMPath = origSys
	}()

	dir := t.TempDir()

	// Create fake /dev/tpm0 and sysfs tree.
	devPath := filepath.Join(dir, "tpm0")
	if err := os.WriteFile(devPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sysDir := filepath.Join(dir, "sys", "tpm0")
	if err := os.MkdirAll(filepath.Join(sysDir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysDir, "tpm_version_major"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysDir, "device", "description"), []byte("TestMfr\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tpmDevicePath = devPath
	tpmrmDevicePath = filepath.Join(dir, "tpmrm0") // does not exist
	sysTPMPath = sysDir

	info := Detect()
	if !info.Present {
		t.Fatal("expected TPM present")
	}
	if !info.DevicePresent {
		t.Error("expected DevicePresent")
	}
	if info.RMPresent {
		t.Error("expected RMPresent=false")
	}
	if info.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", info.Version)
	}
	if info.Manufacturer != "TestMfr" {
		t.Errorf("manufacturer = %q, want TestMfr", info.Manufacturer)
	}
}

func TestDetectNoTPM(t *testing.T) {
	origDev, origRM, origSys := tpmDevicePath, tpmrmDevicePath, sysTPMPath
	defer func() {
		tpmDevicePath = origDev
		tpmrmDevicePath = origRM
		sysTPMPath = origSys
	}()

	dir := t.TempDir()
	tpmDevicePath = filepath.Join(dir, "tpm0")
	tpmrmDevicePath = filepath.Join(dir, "tpmrm0")
	sysTPMPath = filepath.Join(dir, "sys")

	info := Detect()
	if info.Present {
		t.Error("expected TPM not present")
	}
}

func TestReadPCRsWithFixture(t *testing.T) {
	origSys := sysTPMPath
	defer func() { sysTPMPath = origSys }()

	dir := t.TempDir()
	sysDir := filepath.Join(dir, "tpm0")
	pcrDir := filepath.Join(sysDir, "pcr-sha256")
	if err := os.MkdirAll(pcrDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write PCR values as ASCII hex (as sysfs does).
	if err := os.WriteFile(filepath.Join(pcrDir, "0"), []byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pcrDir, "7"), []byte("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sysTPMPath = sysDir
	pcrs, err := ReadPCRs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pcrs) != 2 {
		t.Fatalf("got %d PCRs, want 2", len(pcrs))
	}
	if pcrs[0] != "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Errorf("PCR[0] = %q", pcrs[0])
	}
	if pcrs[7] != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Errorf("PCR[7] = %q", pcrs[7])
	}
}

func TestReadSysfs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_value")
	if err := os.WriteFile(path, []byte("  hello world  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readSysfs(path)
	if got != "hello world" {
		t.Errorf("readSysfs = %q, want %q", got, "hello world")
	}
}

func TestReadSysfsMissing(t *testing.T) {
	got := readSysfs("/nonexistent/path/to/file")
	if got != "" {
		t.Errorf("readSysfs(missing) = %q, want empty", got)
	}
}

func TestPCRSelectMultiple(t *testing.T) {
	tests := []struct {
		name    string
		indices []int
		want    []byte
	}{
		{"empty", nil, []byte{0}},
		{"pcr0", []int{0}, []byte{0x01}},
		{"pcr7", []int{7}, []byte{0x80}},
		{"pcr0_7", []int{0, 7}, []byte{0x81}},
		{"pcr8", []int{8}, []byte{0x00, 0x01}},
		{"pcr0_8", []int{0, 8}, []byte{0x01, 0x01}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pcrSelectMultiple(tc.indices)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("byte[%d] = %x, want %x", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestMarshalQuote(t *testing.T) {
	q := &AttestationQuote{
		QuoteData: []byte{1, 2, 3},
		Signature: []byte{4, 5, 6},
		Nonce:     []byte{7, 8, 9},
	}
	data, err := MarshalQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}
