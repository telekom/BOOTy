//go:build e2e_kvm_secureboot || e2e_kvm_luks || e2e_kvm_tpm || e2e_kvm_kexec || e2e_kvm_bootloader

package kvm

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
