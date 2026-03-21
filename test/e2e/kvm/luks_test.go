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
	requireKVMAssets(t, initramfs, kernel)
	diskImg := envOrDefault("LUKS_DISK_IMAGE", "")
	if diskImg == "" {
		t.Fatal("LUKS_DISK_IMAGE not set")
	}

	extra := splitExtraArgs(envOrDefault("QEMU_EXTRA_ARGS", ""))
	args := make([]string, 0, 16+len(extra))
	args = append(args,
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1",
		"-drive", "file="+diskImg+",format=qcow2,if=virtio",
	)
	args = append(args, extra...)

	out := runQEMUSmoke(t, args, 2*time.Minute, "luks", true)
	t.Logf("LUKS QEMU output (last 500 bytes): %s", tail(out, 500))
}
