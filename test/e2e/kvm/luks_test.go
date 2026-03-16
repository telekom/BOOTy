//go:build e2e

package kvm

import (
	"testing"
	"time"
)

func TestLUKSSmokeQEMU(t *testing.T) {
	qemuAvailable(t)
	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")
	diskImg := envOrDefault("LUKS_DISK_IMAGE", "")
	if diskImg == "" {
		t.Skip("LUKS_DISK_IMAGE not set — skipping LUKS test")
	}

	args := []string{
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1",
	}
	if diskImg != "" {
		args = append(args, "-drive", "file="+diskImg+",format=qcow2,if=virtio")
	}
	args = append(args, splitExtraArgs(envOrDefault("QEMU_EXTRA_ARGS", ""))...)

	out := runQEMUSmoke(t, args, 2*time.Minute, "luks", true)
	t.Logf("LUKS QEMU output (last 500 bytes): %s", tail(out, 500))
}
