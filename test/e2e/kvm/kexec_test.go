//go:build e2e

package kvm

import (
	"testing"
	"time"
)

// TestKexecSmokeQEMU boots BOOTy and confirms it reaches the startup marker.
// This is a prerequisite smoke test ensuring the initramfs boots successfully on a
// kexec-capable kernel. Actual kexec execution (loading a new kernel via kexec_load)
// requires a second kernel image and is not exercised here.
func TestKexecSmokeQEMU(t *testing.T) {
	qemuAvailable(t)
	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")
	requireKVMAssets(t, initramfs, kernel)

	args := []string{
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1",
	}

	out := runQEMUSmoke(t, args, 2*time.Minute, "kexec", true)
	t.Logf("Kexec QEMU output (last 500 bytes): %s", tail(out, 500))
}
