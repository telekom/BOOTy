//go:build linux

package provision

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultRunStreamingCommandStreamsLines(t *testing.T) {
	var lines []string
	err := defaultRunStreamingCommand(context.Background(), func(line string) { lines = append(lines, line) },
		"", []string{"/bin/sh", "-c", "echo one; echo two >&2; printf three"})
	if err != nil {
		t.Fatalf("defaultRunStreamingCommand() = %v", err)
	}
	if !reflect.DeepEqual(lines, []string{"one", "two", "three"}) {
		t.Fatalf("lines = %q", lines)
	}
}

func TestDefaultRunStreamingCommandReportsFailure(t *testing.T) {
	err := defaultRunStreamingCommand(context.Background(), func(string) {}, "", []string{"/bin/sh", "-c", "exit 3"})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("defaultRunStreamingCommand() = %v, want exit status 3", err)
	}
}

func TestDefaultRunStreamingCommandDropsOverlongLines(t *testing.T) {
	var lines []string
	err := defaultRunStreamingCommand(context.Background(), func(line string) { lines = append(lines, line) },
		"", []string{"/bin/sh", "-c", "head -c 70000 /dev/zero | tr '\\0' a; echo; echo after"})
	if err != nil {
		t.Fatalf("defaultRunStreamingCommand() = %v", err)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "output dropped") {
		t.Fatalf("lines = %d, want the too long marker last", len(lines))
	}
}

// A background child that keeps stdout open must not block the step after
// the script itself exited.
func TestDefaultRunStreamingCommandDoesNotWaitForBackgroundChildren(t *testing.T) {
	old := streamWaitDelay
	streamWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { streamWaitDelay = old })

	start := time.Now()
	_ = defaultRunStreamingCommand(context.Background(), func(string) {}, "", []string{"/bin/sh", "-c", "sleep 30 & echo started"})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("command returned after %s, the background child held it", elapsed)
	}
}
