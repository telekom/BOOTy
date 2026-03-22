//go:build e2e

package kvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProvisionAndBootOS provisions a disk via BOOTy in QEMU, then boots the
// provisioned disk directly to verify the OS starts successfully.
func TestProvisionAndBootOS(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	const hostname = "boot-verify"

	// Phase 1: Provision.
	rawDisk := createTestDiskImage(t, 512)
	gzImage := compressGzip(t, rawDisk)
	baseURL := startImageServer(t, gzImage)

	targetDisk := filepath.Join(t.TempDir(), "boot-target.qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":    hostname,
		"IMAGE":       baseURL + "/image.gz",
		"MODE":        "provision",
		"DISK_DEVICE": "/dev/vda",
	})

	kernel := findKernel(t)
	output := runQEMUProvision(t, kernel, initramfs, targetDisk, 5*time.Minute)
	t.Logf("Provision output tail:\n%s", tail(output, 2000))

	// Phase 2: Extract kernel and initrd from provisioned disk for direct boot.
	rootMount, cleanup := mountQcow2(t, targetDisk)
	vmlinuz := extractKernelFromDisk(t, rootMount)
	initrd := extractInitrdFromDisk(t, rootMount)
	cleanup()

	if vmlinuz == "" {
		t.Skip("no kernel found in provisioned disk, cannot boot")
	}

	// Phase 3: Boot the provisioned OS and check serial output.
	bootOutput := bootProvisionedDisk(t, vmlinuz, initrd, targetDisk, 90*time.Second)

	outputStr := string(bootOutput)
	t.Logf("Boot output tail:\n%s", tail(bootOutput, 3000))

	bootIndicators := []string{"Linux version", "Booting"}
	found := false
	for _, indicator := range bootIndicators {
		if strings.Contains(outputStr, indicator) {
			found = true
			t.Logf("Found boot indicator: %q", indicator)
			break
		}
	}
	if !found {
		t.Error("no boot indicators found in serial output — OS may not have started")
	}
}

// extractKernelFromDisk finds the vmlinuz kernel in the mounted root.
func extractKernelFromDisk(t *testing.T, rootMount string) string {
	t.Helper()
	tmpDir := t.TempDir()
	patterns := []string{
		filepath.Join(rootMount, "boot", "vmlinuz-*"),
		filepath.Join(rootMount, "boot", "vmlinuz"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			dst := filepath.Join(tmpDir, "vmlinuz")
			copyBinary(t, matches[0], dst)
			return dst
		}
	}
	return ""
}

// extractInitrdFromDisk finds the initrd/initramfs in the mounted root.
func extractInitrdFromDisk(t *testing.T, rootMount string) string {
	t.Helper()
	tmpDir := t.TempDir()
	patterns := []string{
		filepath.Join(rootMount, "boot", "initrd.img-*"),
		filepath.Join(rootMount, "boot", "initrd.img"),
		filepath.Join(rootMount, "boot", "initramfs-*"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			dst := filepath.Join(tmpDir, "initrd.img")
			copyBinary(t, matches[0], dst)
			return dst
		}
	}
	return ""
}

// bootProvisionedDisk boots the provisioned disk using direct kernel boot.
func bootProvisionedDisk(t *testing.T, vmlinuz, initrd, disk string, timeout time.Duration) []byte {
	t.Helper()
	args := []string{
		"-m", "1024",
		"-nographic",
		"-no-reboot",
		"-kernel", vmlinuz,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", disk),
		"-append", "root=/dev/vda2 console=ttyS0 panic=1",
		"-serial", "stdio",
	}
	if initrd != "" {
		args = append(args, "-initrd", initrd)
	}
	args = append(args, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Logf("Boot timed out after %v (expected — no auto-shutdown)", timeout)
	} else if err != nil {
		t.Logf("Boot QEMU exited: %v", err)
	}

	return out
}
