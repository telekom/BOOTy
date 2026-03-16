//go:build e2e

package kvm

import (
	"testing"
	"time"
)

// TestUEFIBootPathSmoke verifies BOOTy starts under OVMF firmware.
// It is a smoke check, not full GRUB/systemd-boot chain validation.
func TestUEFIBootPathSmoke(t *testing.T) {
	qemuAvailable(t)
	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")
	ovmf := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE.fd")
	ovmfVars := envOrDefault("OVMF_VARS", "")

	args := []string{
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + ovmf,
	}
	if ovmfVars != "" {
		args = append(args, "-drive", "if=pflash,format=raw,file="+ovmfVars)
	}
	args = append(args,
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1",
	)
	args = append(args, splitExtraArgs(envOrDefault("QEMU_EXTRA_ARGS", ""))...)

	out := runQEMUSmoke(t, args, 2*time.Minute, "uefi-boot-path", true)
	t.Logf("Bootloader QEMU output (last 500 bytes): %s", tail(out, 500))
}
