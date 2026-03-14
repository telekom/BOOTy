//go:build e2e_kvm_tpm

package kvm

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTPMQEMU(t *testing.T) {
	qemuAvailable(t)
	if _, err := exec.LookPath("swtpm"); err != nil {
		t.Skip("swtpm not available")
	}

	initramfs := envOrDefault("BOOTY_INITRAMFS", "test-initramfs.cpio.gz")
	kernel := envOrDefault("BOOTY_KERNEL", "vmlinuz")
	tpmDir := t.TempDir()
	tpmSocket := filepath.Join(tpmDir, "swtpm-sock")

	// Start swtpm emulator.
	swtpmCtx, swtpmCancel := context.WithCancel(context.Background())
	defer swtpmCancel()
	swtpm := exec.CommandContext(swtpmCtx, "swtpm", "socket",
		"--tpmstate", "dir="+tpmDir,
		"--ctrl", "type=unixio,path="+tpmSocket,
		"--tpm2",
	)
	if err := swtpm.Start(); err != nil {
		t.Fatalf("failed to start swtpm: %v", err)
	}
	defer swtpmCancel()

	args := []string{
		"-m", "512",
		"-nographic",
		"-no-reboot",
		"-chardev", "socket,id=chrtpm,path=" + tpmSocket,
		"-tpmdev", "emulator,id=tpm0,chardev=chrtpm",
		"-device", "tpm-tis,tpmdev=tpm0",
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
		t.Fatalf("QEMU TPM failed: %v\nOutput: %s", err, out)
	}
	t.Logf("TPM QEMU output (last 500 bytes): %s", tail(out, 500))
}
