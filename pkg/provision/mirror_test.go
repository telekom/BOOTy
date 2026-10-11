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
	stubMirrorTailSpace(t, nil)

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

func stubMirrorTailSpace(t *testing.T, err error) {
	t.Helper()
	orig := checkMirrorTailSpaceFn
	checkMirrorTailSpaceFn = func(string) error { return err }
	t.Cleanup(func() { checkMirrorTailSpaceFn = orig })
}

func TestMirrorRootStopsWithoutTailSpace(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("mdadm --detail", nil, errors.New("no such array"))
	stubMirrorTailSpace(t, errors.New("no room"))

	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk, o.mirrorDisk, o.rootPartition = "/dev/sda", "/dev/sdb", "/dev/sda2"

	if err := o.mirrorRoot(context.Background()); err == nil {
		t.Fatal("mirrorRoot() error = nil, want tail space error")
	}
	for _, call := range cmd.calls {
		if call.name == "sgdisk" || (call.name == "mdadm" && len(call.args) > 0 && call.args[0] == "--create") {
			t.Fatalf("mirrorRoot() ran %s %v after the tail space check failed", call.name, call.args)
		}
	}
}

func TestMirrorRootReusesAssembledArray(t *testing.T) {
	const add = "--add /dev/md2 /dev/sdb2"
	tests := []struct {
		name    string
		detail  string
		wantAdd bool
	}{
		{name: "both members present", detail: "   0  8  2  0  active sync  /dev/sda2\n   1  8  18  1  spare rebuilding  /dev/sdb2\n"},
		{name: "second member missing after failed add", detail: "   0  8  2  0  active sync  /dev/sda2\n   -  0  0  1  removed\n", wantAdd: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newMockCommander()
			cmd.setResult("mdadm --detail", []byte(tt.detail), nil)
			o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
			o.targetDisk, o.mirrorDisk, o.rootPartition = "/dev/sda", "/dev/sdb", "/dev/sda2"

			if err := o.mirrorRoot(context.Background()); err != nil {
				t.Fatalf("mirrorRoot() error = %v", err)
			}
			if o.rootPartition != disk.MirrorRootArray {
				t.Fatalf("rootPartition = %s, want %s", o.rootPartition, disk.MirrorRootArray)
			}
			for _, call := range cmd.calls {
				if call.name == "growpart" || call.name == "sgdisk" || (call.name == "mdadm" && call.args[0] == "--create") {
					t.Fatalf("mirrorRoot() ran %s %v on an assembled array", call.name, call.args)
				}
			}
			if got := hasProvisionCall(cmd.calls, "mdadm", strings.Fields(add)...); got != tt.wantAdd {
				t.Fatalf("mdadm %s called = %v, want %v", add, got, tt.wantAdd)
			}
		})
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

func TestValidateMirroringMode(t *testing.T) {
	raid := []config.RAIDConfig{{Name: "md0", Level: 1, Devices: []string{"/dev/sda", "/dev/sdb"}}}
	tests := []struct {
		name      string
		disk      config.DiskConfig
		imageMode string
		wantErr   bool
	}{
		{name: "unset"},
		{name: "none with explicit raid", disk: config.DiskConfig{MirroringMode: "none", RAID: raid}},
		{name: "raid-1 whole-disk", disk: config.DiskConfig{MirroringMode: "raid-1"}, imageMode: config.ImageModeWholeDisk},
		{name: "auto with explicit disk", disk: config.DiskConfig{MirroringMode: "auto", Device: "/dev/sda"}},
		{name: "unknown mode", disk: config.DiskConfig{MirroringMode: "raid-5"}, wantErr: true},
		{name: "explicit raid", disk: config.DiskConfig{MirroringMode: "raid-1", RAID: raid}, wantErr: true},
		{name: "partition layout", disk: config.DiskConfig{MirroringMode: "auto", PartitionLayout: &config.PartitionLayout{}}, wantErr: true},
		{name: "A/B image mode", disk: config.DiskConfig{MirroringMode: "raid-1"}, imageMode: config.ImageModeAB, wantErr: true},
		{name: "partition image mode", disk: config.DiskConfig{MirroringMode: "auto"}, imageMode: config.ImageModePartition, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk = tt.disk
			cfg.Provision.Image.Mode = tt.imageMode
			o := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
			if err := o.validateMirroringMode(); (err != nil) != tt.wantErr {
				t.Fatalf("validateMirroringMode() error = %v, wantErr %v", err, tt.wantErr)
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
