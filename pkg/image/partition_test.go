//go:build linux

package image

import "testing"

func TestTargetPartitionNode(t *testing.T) {
	tests := []struct {
		name    string
		disk    string
		partNum int
		want    string
	}{
		{"sda partition 1", "/dev/sda", 1, "/dev/sda1"},
		{"sda partition 2", "/dev/sda", 2, "/dev/sda2"},
		{"nvme partition 1", "/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"nvme partition 3", "/dev/nvme0n1", 3, "/dev/nvme0n1p3"},
		{"loop partition 1", "/dev/loop0", 1, "/dev/loop0p1"},
		{"vda partition 2", "/dev/vda", 2, "/dev/vda2"},
		{"mmcblk partition 1", "/dev/mmcblk0", 1, "/dev/mmcblk0p1"},
		{"mmcblk partition 2", "/dev/mmcblk0", 2, "/dev/mmcblk0p2"},
		{"mmcblk1 partition 3", "/dev/mmcblk1", 3, "/dev/mmcblk1p3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := targetPartitionNode(tt.disk, tt.partNum)
			if got != tt.want {
				t.Errorf("targetPartitionNode(%q, %d) = %q, want %q", tt.disk, tt.partNum, got, tt.want)
			}
		})
	}
}

func TestConvertQCOW2HookRegistered(t *testing.T) {
	// On linux, the init() in qcow2.go should have set the hook.
	if convertQCOW2Hook == nil {
		t.Fatal("convertQCOW2Hook is nil on linux")
	}
}

func TestSelectSourcePartitionsForAB(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 4096},
	}
	boot, ok := selectSourceBootPartition(parts)
	if !ok || boot.Node != "/dev/loop0p1" {
		t.Fatalf("boot = %#v, ok=%v", boot, ok)
	}
	root, ok := selectSourceRootPartition(parts)
	if !ok || root.Node != "/dev/loop0p3" {
		t.Fatalf("root = %#v, ok=%v", root, ok)
	}
}

func TestSelectSourceRootPartitionFallsBackToLargestNonEFI(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: "unknown", Size: 8192},
		{Node: "/dev/loop0p3", Type: "unknown", Size: 4096},
	}
	root, ok := selectSourceRootPartition(parts)
	if !ok || root.Node != "/dev/loop0p2" {
		t.Fatalf("root = %#v, ok=%v", root, ok)
	}
}
