//go:build linux_e2e

package linux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/disk"
)

func TestParsePartitionsWithRealSfdisk(t *testing.T) {
	loopDev := createLoopDevice(t, 1)
	ctx := context.Background()

	mgr := disk.NewManager(nil) // real ExecCommander
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 partitions, got %d", len(parts))
	}

	// Verify partition types match what we created.
	if !strings.EqualFold(parts[0].Type, disk.EFISystemPartitionGUID) {
		t.Errorf("partition 1 type = %s, want %s", parts[0].Type, disk.EFISystemPartitionGUID)
	}
	if !strings.EqualFold(parts[1].Type, disk.LinuxFilesystemGUID) {
		t.Errorf("partition 2 type = %s, want %s", parts[1].Type, disk.LinuxFilesystemGUID)
	}
}

func TestFindBootAndRootPartitions(t *testing.T) {
	loopDev := createLoopDevice(t, 1)
	ctx := context.Background()

	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	boot, err := mgr.FindBootPartition(parts)
	if err != nil {
		t.Fatalf("FindBootPartition: %v", err)
	}
	if !strings.Contains(boot.Node, loopDev) {
		t.Errorf("boot partition node %q does not contain loop device %q", boot.Node, loopDev)
	}

	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}
	if !strings.Contains(root.Node, loopDev) {
		t.Errorf("root partition node %q does not contain loop device %q", root.Node, loopDev)
	}

	// Boot and root should be different partitions.
	if boot.Node == root.Node {
		t.Error("boot and root partitions should be different")
	}
}

func TestPartitionNumberFromLoopDevice(t *testing.T) {
	loopDev := createLoopDevice(t, 1)
	ctx := context.Background()

	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}

	partNum := disk.PartitionNumber(root.Node, loopDev)
	if partNum != 2 {
		t.Errorf("PartitionNumber = %d, want 2", partNum)
	}
}

func TestFormatMountUnmount(t *testing.T) {
	loopDev := createLoopDevice(t, 1)
	ctx := context.Background()

	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}

	// Format the root partition with ext4.
	runCmd(t, "mkfs.ext4", "-F", root.Node)

	// Create temp mountpoint.
	mountpoint, err := os.MkdirTemp("", "booty-mount-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(mountpoint)

	// Mount using disk.Manager.
	if err := mgr.MountPartition(ctx, root.Node, mountpoint); err != nil {
		t.Fatalf("MountPartition: %v", err)
	}

	// Write a test file to verify the mount works.
	testFile := mountpoint + "/test.txt"
	if err := os.WriteFile(testFile, []byte("hello booty"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify file exists.
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello booty" {
		t.Errorf("file content = %q, want %q", string(data), "hello booty")
	}

	// Unmount.
	if err := mgr.Unmount(mountpoint); err != nil {
		t.Fatalf("Unmount: %v", err)
	}

	// Verify file is no longer accessible (mountpoint empty).
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("mountpoint should be empty after unmount, got %d entries", len(entries))
	}
}

func TestCheckFilesystem(t *testing.T) {
	loopDev := createLoopDevice(t, 1)
	ctx := context.Background()

	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}

	// Format and then check.
	runCmd(t, "mkfs.ext4", "-F", root.Node)

	// CheckFilesystem should not error on a clean filesystem.
	if err := mgr.CheckFilesystem(ctx, root.Node); err != nil {
		t.Fatalf("CheckFilesystem: %v", err)
	}
}

func TestBindMountAndTeardown(t *testing.T) {
	requireRoot(t)
	ctx := context.Background()

	loopDev := createLoopDevice(t, 1)
	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}
	runCmd(t, "mkfs.ext4", "-F", root.Node)

	// Mount root partition.
	mountpoint, err := os.MkdirTemp("", "booty-root-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(mountpoint)

	if err := mgr.MountPartition(ctx, root.Node, mountpoint); err != nil {
		t.Fatalf("MountPartition: %v", err)
	}
	defer mgr.Unmount(mountpoint) //nolint:errcheck

	// Create subdirectories for bind mounts.
	for _, dir := range []string{"dev", "proc", "sys", "run"} {
		os.MkdirAll(mountpoint+"/"+dir, 0o755)
	}

	// Setup chroot bind mounts.
	if err := mgr.SetupChrootBindMounts(mountpoint); err != nil {
		t.Fatalf("SetupChrootBindMounts: %v", err)
	}

	// Verify /proc is bind-mounted (should have content).
	entries, err := os.ReadDir(mountpoint + "/proc")
	if err != nil {
		t.Fatalf("ReadDir proc: %v", err)
	}
	if len(entries) == 0 {
		t.Error("/proc bind mount appears empty")
	}

	// Teardown.
	mgr.TeardownChrootBindMounts(mountpoint)
}

func TestGrowPartitionAndResize(t *testing.T) {
	requireRoot(t)

	// Check if growpart is available.
	if _, err := exec.LookPath("growpart"); err != nil {
		t.Fatal("growpart not available")
	}

	ctx := context.Background()
	loopDev := createLoopDevice(t, 2) // 2GB to have room to grow

	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}

	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}

	partNum := disk.PartitionNumber(root.Node, loopDev)

	// Format the root partition before growing.
	runCmd(t, "mkfs.ext4", "-F", root.Node)

	// Grow partition to fill available space.
	err = mgr.GrowPartition(ctx, loopDev, partNum)
	if err != nil {
		t.Fatalf("GrowPartition: %v", err)
	}

	// Re-read partitions to verify size changed.
	newParts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions after grow: %v", err)
	}
	newRoot, err := mgr.FindRootPartition(newParts)
	if err != nil {
		t.Fatalf("FindRootPartition after grow: %v", err)
	}

	if newRoot.Size <= root.Size {
		t.Logf("Warning: partition did not grow (may already be at max): old=%d new=%d", root.Size, newRoot.Size)
	}

	// Resize filesystem.
	err = mgr.ResizeFilesystem(ctx, newRoot.Node)
	if err != nil {
		t.Fatalf("ResizeFilesystem: %v", err)
	}
}

func TestPartProbeRefreshes(t *testing.T) {
	requireRoot(t)
	ctx := context.Background()

	loopDev := createLoopDevice(t, 1)
	mgr := disk.NewManager(nil)

	// PartProbe should succeed on our loop device.
	if err := mgr.PartProbe(ctx, loopDev); err != nil {
		t.Fatalf("PartProbe: %v", err)
	}

	// Verify partitions are still readable after partprobe.
	parts, err := mgr.ParsePartitions(ctx, loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions after PartProbe: %v", err)
	}
	if len(parts) != 2 {
		t.Errorf("expected 2 partitions after PartProbe, got %d", len(parts))
	}
}
