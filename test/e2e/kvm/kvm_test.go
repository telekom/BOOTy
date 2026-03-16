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

// splitExtraArgs splits an environment variable into separate arguments.
func splitExtraArgs(env string) []string {
	if env == "" {
		return nil
	}
	return strings.Fields(env)
}

// tail returns the last n bytes of data.
func tail(data []byte, n int) []byte {
	if len(data) <= n {
		return data
	}
	return data[len(data)-n:]
}

// runQEMUSmoke launches QEMU and requires BOOTy startup marker in serial output.
func runQEMUSmoke(t *testing.T, args []string, timeout time.Duration, scenario string) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		if !strings.Contains(outStr, bootyStartMarker) {
			t.Fatalf("QEMU %s timed out before BOOTy marker. tail:\n%s", scenario, tail(out, 1200))
		}
		t.Logf("QEMU %s timed out after BOOTy marker (acceptable for smoke)", scenario)
		return out
	}

	if err != nil {
		t.Fatalf("QEMU %s failed: %v\nOutput:\n%s", scenario, err, out)
	}

	if !strings.Contains(outStr, bootyStartMarker) {
		t.Fatalf("QEMU %s completed without BOOTy marker. tail:\n%s", scenario, tail(out, 1200))
	}

	return out
}
