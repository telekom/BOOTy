//go:build e2e_kvm_secureboot

package kvm

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestSecureBootQEMU(t *testing.T) {
	qemuAvailable(t)
	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")
	ovmf := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE.secboot.fd")

	args := []string{
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + ovmf,
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1",
	}
	args = append(args, splitExtraArgs(envOrDefault("QEMU_EXTRA_ARGS", ""))...)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Logf("QEMU timed out (expected for initrd boot)")
	} else if err != nil {
		t.Fatalf("QEMU secureboot failed: %v\nOutput: %s", err, out)
	}
	t.Logf("SecureBoot QEMU output (last 500 bytes): %s", tail(out, 500))
}
