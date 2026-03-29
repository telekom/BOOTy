//go:build linux

package debug

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadFileMissingReturnsEmpty(t *testing.T) {
	if got := readFile("/path/does/not/exist"); got != "" {
		t.Fatalf("readFile() = %q, want empty string", got)
	}
}

func TestReadFileTrimsWhitespace(t *testing.T) {
	file := t.TempDir() + "/value.txt"
	content := "value with spaces\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	if got := readFile(file); got != "value with spaces" {
		t.Fatalf("readFile() = %q, want %q", got, "value with spaces")
	}
}

func TestRunCmdSuccess(t *testing.T) {
	got := runCmd(context.Background(), "sh", "-c", "printf hello")
	if got != "hello" {
		t.Fatalf("runCmd() = %q, want %q", got, "hello")
	}
}

func TestRunCmdFailureReturnsEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got := runCmd(ctx, "sh", "-c", "printf hello"); got != "" {
		t.Fatalf("runCmd() = %q, want empty string on command failure", got)
	}
}

func TestMarshal(t *testing.T) {
	d := &Dump{System: SystemSnapshot{Hostname: "node-1"}}
	data, err := d.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	if !strings.Contains(string(data), "node-1") {
		t.Fatalf("Marshal() output missing hostname: %s", string(data))
	}
}

func TestCollectReturnsNonNilDump(t *testing.T) {
	d := Collect(context.Background())
	if d == nil {
		t.Fatal("Collect() returned nil")
	}
}
