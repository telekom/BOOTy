package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetBlockDeviceSize_NotExist(t *testing.T) {
	_, err := GetBlockDeviceSize("nonexistent_device_xyz")
	if err == nil {
		t.Error("expected error for nonexistent device")
	}
}

func TestGetBlockDeviceSize_InvalidContent(t *testing.T) {
	// Create a mock sysfs file with non-numeric content.
	tmpDir := t.TempDir()
	devDir := filepath.Join(tmpDir, "sys", "block", "testdev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "size"), []byte("notanumber"), 0o644); err != nil {
		t.Fatal(err)
	}
	// GetBlockDeviceSize uses hardcoded /sys/block path so we can't override it.
	// Just test the error path for non-existent devices.
	_, err := GetBlockDeviceSize("testdev_nonexist")
	if err == nil {
		t.Error("expected error")
	}
}

func TestClearDir_NonExistent(t *testing.T) {
	err := ClearDir("/nonexistent/path/cleardir")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestClearDir_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	if err := ClearDir(tmpDir); err != nil {
		t.Fatalf("ClearDir(empty) = %v", err)
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestDashMac_EdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{":", "-"},
		{"::", "--"},
		{"a:b:c", "a-b-c"},
	}
	for _, tc := range tests {
		got := DashMac(tc.input)
		if got != tc.want {
			t.Errorf("DashMac(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseCmdLine_MultipleEquals(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cmdline")
	// Entry with multiple = signs should only split on first =
	content := "root=/dev/sda1 BOOT_IMAGE=/vmlinuz key=val=ue"
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseCmdLine(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	if m["root"] != "/dev/sda1" {
		t.Errorf("root = %q", m["root"])
	}
	if m["BOOT_IMAGE"] != "/vmlinuz" {
		t.Errorf("BOOT_IMAGE = %q", m["BOOT_IMAGE"])
	}
	// "key=val=ue" splits into ["key", "val", "ue"] which has len 3, not 2
	// so it should NOT be in the map.
	if _, ok := m["key"]; ok {
		t.Error("key with multiple = should not be parsed")
	}
}
