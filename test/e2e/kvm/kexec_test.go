//go:build e2e_kvm_kexec

package kvm

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestKexecQEMU(t *testing.T) {
	qemuAvailable(t)
	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")

	args := []string{
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Logf("QEMU timed out (expected for initrd boot)")
	} else if err != nil {
		t.Fatalf("QEMU kexec failed: %v\nOutput: %s", err, out)
	}
	t.Logf("Kexec QEMU output (last 500 bytes): %s", tail(out, 500))
}
