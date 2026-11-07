package health

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemInfoMiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	data := "MemTotal:        2048000 kB\nMemFree:          100000 kB\nMemAvailable:    1024000 kB\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if total, available := MemInfoMiB(path); total != 2000 || available != 1000 {
		t.Fatalf("MemInfoMiB = %d, %d, want 2000, 1000", total, available)
	}
	if total, available := MemInfoMiB(filepath.Join(t.TempDir(), "missing")); total != 0 || available != 0 {
		t.Fatalf("missing file: %d, %d, want zeros", total, available)
	}
}
