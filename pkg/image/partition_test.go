//go:build linux

package image

import (
	"context"
	"strings"
	"testing"
)

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

func TestReadSfdiskPartitionsDerivesNumbersFromDeviceNodes(t *testing.T) {
	previous := runCmd
	runCmd = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "sfdisk" || len(args) != 2 || args[0] != "--json" || args[1] != "/dev/loop0" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		return []byte(`sfdisk noise
{"partitiontable":{"partitions":[
  {"node":"/dev/loop0p5","start":2048,"size":4096,"type":"83"},
  {"node":"/dev/loop0p7","start":6144,"size":4096,"type":"83"},
  {"node":"/dev/mapper/root","start":10240,"size":4096,"type":"83"}
]}}`), nil
	}
	t.Cleanup(func() { runCmd = previous })

	parts, err := readSfdiskPartitions(context.Background(), "/dev/loop0")
	if err != nil {
		t.Fatalf("readSfdiskPartitions: %v", err)
	}
	got := []int{parts[0].Number, parts[1].Number, parts[2].Number}
	want := []int{5, 7, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("partition numbers = %v, want %v", got, want)
		}
	}
}

func TestSelectSourcePartitionsForAB(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Number: 1},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048, Name: "rootfs", Number: 2},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 4096, Name: "data", Number: 3},
	}
	boot, ok := selectSourceBootPartition(parts)
	if !ok || boot.Node != "/dev/loop0p1" {
		t.Fatalf("boot = %#v, ok=%v", boot, ok)
	}
	root, err := selectSourceRootPartition(parts, "", 0)
	if err != nil || root.Node != "/dev/loop0p2" {
		t.Fatalf("root = %#v, err=%v", root, err)
	}
}

func TestSelectSourceRootPartitionRejectsAmbiguousLinuxPartitions(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 8192},
	}
	if _, err := selectSourceRootPartition(parts, "", 0); err == nil {
		t.Fatal("expected ambiguous Linux root selection to fail")
	}
}

func TestSelectSourceRootPartitionSupportsExplicitSelectors(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Number: 1},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048, Name: "ROOT-A", Number: 2},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 8192, Name: "STATE", Number: 3},
	}

	root, err := selectSourceRootPartition(parts, "root-a", 0)
	if err != nil || root.Node != "/dev/loop0p2" {
		t.Fatalf("label root = %#v, err=%v", root, err)
	}

	root, err = selectSourceRootPartition(parts, "", 3)
	if err != nil || root.Node != "/dev/loop0p3" {
		t.Fatalf("partition root = %#v, err=%v", root, err)
	}
}

func TestSelectSourceRootPartitionRejectsExplicitEFI(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Number: 1},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048, Name: "rootfs", Number: 2},
	}

	_, err := selectSourceRootPartition(parts, "", 1)
	if err == nil {
		t.Fatal("expected explicit EFI partition selector to fail")
	}
	if !strings.Contains(err.Error(), "is EFI, not a root partition") {
		t.Fatalf("error = %q, want EFI rejection", err.Error())
	}
}

func TestSelectSourceRootPartitionAllowsSingleNonEFI(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: "unknown", Size: 8192},
	}
	root, err := selectSourceRootPartition(parts, "", 0)
	if err != nil || root.Node != "/dev/loop0p2" {
		t.Fatalf("root = %#v, err=%v", root, err)
	}
}
