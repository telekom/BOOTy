//go:build e2e

package kvm

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const bootyStartMarker = "Starting BOOTy"

// qemuAvailable checks if qemu-system-x86_64 is installed.
func qemuAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		t.Skip("qemu-system-x86_64 not available")
	}
}

// envOrDefault returns the environment variable value or a default.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitExtraArgs splits a shell-style argument string into separate arguments,
// respecting single and double quotes so values like "-append 'console=ttyS0 panic=1'"
// are parsed correctly.
func splitExtraArgs(env string) []string {
	if env == "" {
		return nil
	}

	var args []string
	var current strings.Builder
	var quote rune
	for _, r := range env {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// tail returns the last n bytes of data.
func tail(data []byte, n int) []byte {
	if len(data) <= n {
		return data
	}
	return data[len(data)-n:]
}

// runQEMUSmoke launches QEMU and optionally requires BOOTy startup marker in serial output.
func runQEMUSmoke(t *testing.T, args []string, timeout time.Duration, scenario string, requireBootyMarker bool) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		if requireBootyMarker && !strings.Contains(outStr, bootyStartMarker) {
			t.Fatalf("QEMU %s timed out before BOOTy marker. tail:\n%s", scenario, tail(out, 1200))
		}
		if requireBootyMarker {
			t.Logf("QEMU %s timed out after BOOTy marker (acceptable for smoke)", scenario)
		} else {
			t.Logf("QEMU %s timed out during firmware-path smoke", scenario)
		}
		return out
	}

	if err != nil {
		t.Fatalf("QEMU %s failed: %v\nOutput:\n%s", scenario, err, out)
	}

	if requireBootyMarker && !strings.Contains(outStr, bootyStartMarker) {
		t.Fatalf("QEMU %s completed without BOOTy marker. tail:\n%s", scenario, tail(out, 1200))
	}

	return out
}
