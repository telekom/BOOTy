//go:build linux

package tpm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectNoTPM(t *testing.T) {
	info := Detect()
	if info.Present {
		t.Logf("TPM found: version=%s manufacturer=%s", info.Version, info.Manufacturer)
	} else {
		t.Log("No TPM device found (expected in CI)")
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
