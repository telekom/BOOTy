//go:build e2e

package kvm

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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
		t.Skip("LUKS_DISK_IMAGE not set")
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

// TestLUKSVerifyHeader verifies that the LUKS test disk has a valid LUKS header.
// This ensures the CI-generated LUKS disk is properly formatted before being
// used in smoke tests.
func TestLUKSVerifyHeader(t *testing.T) {
	diskImg := envOrDefault("LUKS_DISK_IMAGE", "")
	if diskImg == "" {
		t.Skip("LUKS_DISK_IMAGE not set")
	}

	if _, err := os.Stat(diskImg); err != nil {
		t.Fatalf("LUKS disk image not found: %v", err)
	}

	if _, err := exec.LookPath("cryptsetup"); err != nil {
		t.Skip("cryptsetup not available")
	}

	// Verify LUKS header via cryptsetup luksDump (works on disk images directly).
	// For qcow2, we need to use qemu-nbd first.
	if _, err := exec.LookPath("qemu-nbd"); err != nil {
		t.Skip("qemu-nbd not available for LUKS header inspection")
	}

	requireRoot(t)

	// Connect qcow2 to nbd device.
	cmd := exec.Command("modprobe", "nbd", "max_part=8")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("modprobe nbd: %v\n%s", err, out)
	}

	nbdDev := ""
	for i := 0; i < 16; i++ {
		dev := fmt.Sprintf("/dev/nbd%d", i)
		cmd := exec.Command("qemu-nbd", "--connect="+dev, diskImg)
		if _, err := cmd.CombinedOutput(); err == nil {
			nbdDev = dev
			break
		}
	}
	if nbdDev == "" {
		t.Fatal("no free nbd device found")
	}
	t.Cleanup(func() {
		_ = exec.Command("qemu-nbd", "--disconnect", nbdDev).Run()
	})

	// Wait for the nbd device to become ready.
	waitForDevice(t, nbdDev, 10*time.Second)

	// Run cryptsetup luksDump to verify LUKS header.
	out, err := exec.Command("cryptsetup", "luksDump", nbdDev).CombinedOutput()
	if err != nil {
		t.Fatalf("cryptsetup luksDump failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "LUKS header information") {
		t.Errorf("LUKS header not found in luksDump output:\n%s", output)
	}

	t.Logf("LUKS header verified:\n%s", output[:min(len(output), 500)])
}
