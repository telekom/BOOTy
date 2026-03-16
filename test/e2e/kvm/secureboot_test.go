//go:build e2e

package kvm

import (
	"os"
	"testing"
	"time"
)

// TestUEFISecureBootSmoke verifies BOOTy starts with secureboot-enabled OVMF firmware.
// Note: This uses QEMU's -kernel/-initrd direct Linux boot path which bypasses the
// UEFI firmware Secure Boot signature-chain. This is intentional — the test validates
// that BOOTy's initramfs functions in the presence of Secure Boot firmware (e.g. efivar
// visibility, TPM interactions), not that the boot chain itself is signed. Full
// bootloader-chain validation requires a signed shim/GRUB and a bootable disk image,
// which is covered by the ISO boot tests.
func TestUEFISecureBootSmoke(t *testing.T) {
	qemuAvailable(t)
	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")
	requireKVMAssets(t, initramfs, kernel)
	ovmf := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE.secboot.fd")
	ovmfVars := envOrDefault("OVMF_VARS", "")

	if _, err := os.Stat(ovmf); err != nil {
		t.Skipf("OVMF Secure Boot firmware not found at %s — skipping test", ovmf)
	}

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

	out := runQEMUSmoke(t, args, 2*time.Minute, "uefi-secureboot-firmware", false)
	t.Logf("SecureBoot QEMU output (last 500 bytes): %s", tail(out, 500))
}
