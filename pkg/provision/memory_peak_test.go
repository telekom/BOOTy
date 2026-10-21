//go:build linux

package provision

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartMemoryPeakLogsLowestAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	write := func(availableKB string) {
		t.Helper()
		data := "MemTotal:        2048000 kB\nMemAvailable:    " + availableKB + " kB\n"
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldInfo, oldInterval := memoryPeakInfo, memoryPeakInterval
	memoryPeakInfo, memoryPeakInterval = path, time.Hour
	t.Cleanup(func() { memoryPeakInfo, memoryPeakInterval = oldInfo, oldInterval })

	write("1536000") // 1500 MiB available at the start
	var buf bytes.Buffer
	stop := startMemoryPeak(slog.New(slog.NewTextHandler(&buf, nil)))
	write("512000") // 500 MiB available at the end: the final sample
	stop()

	if got := buf.String(); !strings.Contains(got, "peak_used_mib=1500") || !strings.Contains(got, "min_available_mib=500") {
		t.Fatalf("log = %q, want peak_used_mib=1500 min_available_mib=500", got)
	}
}

func TestStartMemoryPeakWithoutMeminfo(t *testing.T) {
	old := memoryPeakInfo
	memoryPeakInfo = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { memoryPeakInfo = old })
	var buf bytes.Buffer
	startMemoryPeak(slog.New(slog.NewTextHandler(&buf, nil)))()
	if buf.Len() != 0 {
		t.Fatalf("logged %q without meminfo", buf.String())
	}
}
