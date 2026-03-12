package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDashMac(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"00:11:22:33:44:55", "00-11-22-33-44-55"},
		{"aa:bb:cc:dd:ee:ff", "aa-bb-cc-dd-ee-ff"},
		{"no-colons", "no-colons"},
		{"", ""},
	}
	for _, tt := range tests {
		result := DashMac(tt.input)
		if result != tt.expected {
			t.Errorf("DashMac(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestParseCmdLine(t *testing.T) {
	// Create a temporary file with cmdline content
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "cmdline")

	content := "root=/dev/sda1 console=ttyS0 quiet splash"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	m, err := ParseCmdLine(tmpFile)
	if err != nil {
		t.Fatalf("ParseCmdLine() error: %v", err)
	}

	expected := map[string]string{
		"root":    "/dev/sda1",
		"console": "ttyS0",
	}

	for k, v := range expected {
		if m[k] != v {
			t.Errorf("ParseCmdLine()[%q] = %q, want %q", k, m[k], v)
		}
	}

	// "quiet" and "splash" have no = so should not appear
	if _, ok := m["quiet"]; ok {
		t.Error("ParseCmdLine() should not include entries without '='")
	}
}

func TestParseCmdLineEmptyPath(t *testing.T) {
	// With empty path, it defaults to /proc/cmdline which may not exist in test env
	_, err := ParseCmdLine("")
	// We just verify it doesn't panic; error is OK if /proc/cmdline doesn't exist
	_ = err
}

func TestParseCmdLineNonExistent(t *testing.T) {
	_, err := ParseCmdLine("/nonexistent/path/cmdline")
	if err == nil {
		t.Error("ParseCmdLine() with non-existent path should return error")
	}
}

func TestClearDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some files
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Create a subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	if err := ClearDir(tmpDir); err != nil {
		t.Fatalf("ClearDir() error: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read dir after ClearDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ClearDir() left %d entries, want 0", len(entries))
	}
}

func TestClearDirNonExistent(t *testing.T) {
	err := ClearDir("/nonexistent/dir")
	if err == nil {
		t.Error("ClearDir() with non-existent dir should return error")
	}
}

func TestGetBlockDeviceSize(t *testing.T) {
	// Create a fake /sys/block/<dev>/size file
	tmpDir := t.TempDir()
	sizeDir := filepath.Join(tmpDir, "fakedev")
	if err := os.MkdirAll(sizeDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write "2048\n" meaning 2048 sectors * 512 = 1048576 bytes
	if err := os.WriteFile(filepath.Join(sizeDir, "size"), []byte("2048\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// We can't easily override the /sys/block path, so just test the error case
	_, err := GetBlockDeviceSize("nonexistent_device_xyz")
	if err == nil {
		t.Error("GetBlockDeviceSize() with non-existent device should return error")
	}
}

func TestClearScreen(t *testing.T) {
	// Smoke test: should not panic
	ClearScreen()
}
