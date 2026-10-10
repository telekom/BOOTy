//go:build linux

package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

func hasProvisionCall(calls []mockCall, name string, args ...string) bool {
	want := strings.Join(args, " ")
	for _, call := range calls {
		if call.name == name && strings.Join(call.args, " ") == want {
			return true
		}
	}
	return false
}

func TestMirrorRootBuildsRAID1AcrossBothDisks(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("mdadm --detail", nil, errors.New("no such array"))
	var copied []string
	orig := copyBlockDeviceFn
	copyBlockDeviceFn = func(src, dst string) error {
		copied = append(copied, src+"->"+dst)
		return nil
	}
	t.Cleanup(func() { copyBlockDeviceFn = orig })

	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk, o.mirrorDisk = "/dev/sda", "/dev/sdb"
	o.rootPartition, o.bootPartition = "/dev/sda2", "/dev/sda1"

	if err := o.mirrorRoot(context.Background()); err != nil {
		t.Fatalf("mirrorRoot() error = %v", err)
	}
	if o.rootPartition != disk.MirrorRootArray {
		t.Fatalf("rootPartition = %s, want %s", o.rootPartition, disk.MirrorRootArray)
	}
	if len(copied) != 1 || copied[0] != "/dev/sda1->/dev/sdb1" {
		t.Fatalf("EFI copy = %v, want /dev/sda1->/dev/sdb1", copied)
	}
	for _, want := range [][]string{
		{"growpart", "--update", "on", "/dev/sda", "2"},
		{"sgdisk", "--replicate=/dev/sdb", "/dev/sda"},
		{"mdadm", "--create", "/dev/md2", "--run", "--level=1", "--raid-devices=2", "--metadata=1.0", "--assume-clean", "/dev/sda2", "missing"},
		{"mdadm", "--add", "/dev/md2", "/dev/sdb2"},
	} {
		if !hasProvisionCall(cmd.calls, want[0], want[1:]...) {
			t.Errorf("missing command %v", want)
		}
	}
}

func TestMirrorRootReusesAssembledArray(t *testing.T) {
	cmd := newMockCommander()
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk, o.mirrorDisk, o.rootPartition = "/dev/sda", "/dev/sdb", "/dev/sda2"

	if err := o.mirrorRoot(context.Background()); err != nil {
		t.Fatalf("mirrorRoot() error = %v", err)
	}
	if o.rootPartition != disk.MirrorRootArray {
		t.Fatalf("rootPartition = %s, want %s", o.rootPartition, disk.MirrorRootArray)
	}
	if hasProvisionCall(cmd.calls, "mdadm", "--create", "/dev/md2", "--run", "--level=1", "--raid-devices=2",
		"--metadata=1.0", "--assume-clean", "/dev/sda2", "missing") {
		t.Fatal("mirrorRoot() recreated an already assembled array")
	}
}

func TestMirrorRootWithoutMirrorDiskIsNoop(t *testing.T) {
	cmd := newMockCommander()
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk, o.rootPartition = "/dev/sda", "/dev/sda2"
	if err := o.mirrorRoot(context.Background()); err != nil {
		t.Fatalf("mirrorRoot() error = %v", err)
	}
	if len(cmd.calls) != 0 || o.rootPartition != "/dev/sda2" {
		t.Fatalf("mirrorRoot() ran %v and set root %s for a single disk", cmd.calls, o.rootPartition)
	}
}

func TestSelectMirrorPairRejectsInvalidCombinations(t *testing.T) {
	for name, disk := range map[string]config.DiskConfig{
		"unknown mode": {MirroringMode: "raid-5"},
		"explicit raid": {MirroringMode: "raid-1", RAID: []config.RAIDConfig{
			{Name: "md0", Level: 1, Devices: []string{"/dev/sda", "/dev/sdb"}},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk = disk
			o := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
			if _, err := o.selectMirrorPair(context.Background()); err == nil {
				t.Fatal("selectMirrorPair() error = nil, want error")
			}
		})
	}
}

func TestSelectMirrorPairDisabled(t *testing.T) {
	for _, mode := range []string{"", "none", "NONE"} {
		cfg := &config.MachineConfig{}
		cfg.Provision.Disk.MirroringMode = mode
		o := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
		mirrored, err := o.selectMirrorPair(context.Background())
		if err != nil || mirrored {
			t.Fatalf("selectMirrorPair(%q) = %v, %v; want false, nil", mode, mirrored, err)
		}
	}
}

func newTestDiskManager() *disk.Manager {
	return disk.NewManager(newMockCommander())
}
