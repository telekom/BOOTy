//go:build linux

package disk

import (
	"context"
	"testing"
)

func TestDetectMirrorPairPicksLargestEqualPair(t *testing.T) {
	sysfs := t.TempDir()
	writeSysfsBlockDevice(t, sysfs, "sda", "0", 100, "")
	writeSysfsBlockDevice(t, sysfs, "sdb", "0", 20, "")
	writeSysfsBlockDevice(t, sysfs, "sdc", "0", 20, "")
	writeSysfsBlockDevice(t, sysfs, "sdd", "0", 50, "")
	writeSysfsBlockDevice(t, sysfs, "sde", "0", 50, "")
	writeSysfsBlockDevice(t, sysfs, "md2", "0", 50, "")

	first, second, ok, err := newManagerWithSysfs(newMockCommander(), sysfs).DetectMirrorPair(context.Background(), 0)
	if err != nil || !ok {
		t.Fatalf("DetectMirrorPair() ok=%v err=%v, want a pair", ok, err)
	}
	if first != "/dev/sdd" || second != "/dev/sde" {
		t.Fatalf("DetectMirrorPair() = %s, %s; want /dev/sdd, /dev/sde", first, second)
	}
}

func TestDetectMirrorPairWithoutEqualDisks(t *testing.T) {
	sysfs := t.TempDir()
	writeSysfsBlockDevice(t, sysfs, "sda", "0", 100, "")
	writeSysfsBlockDevice(t, sysfs, "sdb", "0", 20, "")
	writeSysfsBlockDevice(t, sysfs, "sdc", "1", 20, "") // removable

	_, _, ok, err := newManagerWithSysfs(newMockCommander(), sysfs).DetectMirrorPair(context.Background(), 0)
	if err != nil || ok {
		t.Fatalf("DetectMirrorPair() ok=%v err=%v, want no pair", ok, err)
	}
}

func TestDetectMirrorPairHonorsMinimumSize(t *testing.T) {
	sysfs := t.TempDir()
	writeSysfsBlockDevice(t, sysfs, "sda", "0", 20, "")
	writeSysfsBlockDevice(t, sysfs, "sdb", "0", 20, "")

	_, _, ok, err := newManagerWithSysfs(newMockCommander(), sysfs).DetectMirrorPair(context.Background(), 30)
	if err != nil || ok {
		t.Fatalf("DetectMirrorPair() ok=%v err=%v, want no pair above 30 GiB", ok, err)
	}
}

func TestMirrorCommands(t *testing.T) {
	cmd := newMockCommander()
	m := NewManager(cmd)
	ctx := context.Background()

	if err := m.ReplicatePartitionTable(ctx, "/dev/sda", "/dev/sdb"); err != nil {
		t.Fatalf("ReplicatePartitionTable() error = %v", err)
	}
	if err := m.CreateDegradedMirror(ctx, MirrorRootArray, "/dev/sda2"); err != nil {
		t.Fatalf("CreateDegradedMirror() error = %v", err)
	}
	if err := m.AddMirrorMember(ctx, MirrorRootArray, "/dev/sdb2"); err != nil {
		t.Fatalf("AddMirrorMember() error = %v", err)
	}

	for _, want := range [][]string{
		{"sgdisk", "--replicate=/dev/sdb", "/dev/sda"},
		{"sgdisk", "--randomize-guids", "/dev/sdb"},
		{"mdadm", "--create", "/dev/md2", "--run", "--level=1", "--raid-devices=2", "--metadata=1.0", "--assume-clean", "/dev/sda2", "missing"},
		{"mdadm", "--add", "/dev/md2", "/dev/sdb2"},
	} {
		if !hasDiskCommandCall(cmd.calls, want[0], want[1:]...) {
			t.Errorf("missing command %v in %v", want, cmd.calls)
		}
	}
}
