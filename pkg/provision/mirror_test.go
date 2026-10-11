//go:build linux

package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if len(copied) != 0 {
		t.Fatalf("mirrorRoot() copied the ESP %v; create-mirror-efi-boot-entry copies it after the last ESP write", copied)
	}
	for _, want := range [][]string{
		{"growpart", "--update", "on", "/dev/sda", "2"},
		{"sgdisk", "--replicate=/dev/sdb", "/dev/sda"},
		{"wipefs", "-af", "/dev/sdb2"},
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
		{name: "NVMe namespace setup", disk: config.DiskConfig{MirroringMode: "raid-1", NVMeNamespaces: "2"}, wantErr: true},
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

func TestCreateMirrorEFIBootEntryBIOSInstallsGrubAndCopiesESP(t *testing.T) {
	var events []string
	origCopy, origMounted, origMount, origEFI := copyBlockDeviceFn, isMountPoint, mountBootPart, efiFirmwarePath
	t.Cleanup(func() {
		copyBlockDeviceFn, isMountPoint, mountBootPart, efiFirmwarePath = origCopy, origMounted, origMount, origEFI
	})
	efiFirmwarePath = filepath.Join(t.TempDir(), "missing-efi")
	isMountPoint = func(string) bool { return false }
	copyBlockDeviceFn = func(src, dst string) error {
		events = append(events, "copy "+src+"->"+dst)
		return nil
	}
	mountBootPart = func(_ context.Context, _ *disk.Manager, device, mountpoint string) error {
		events = append(events, "mount "+device+" "+mountpoint)
		return nil
	}

	cmd := newMockCommander()
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk, o.mirrorDisk, o.bootPartition = "/dev/sda", "/dev/sdb", "/dev/sda1"
	if err := o.createMirrorEFIBootEntry(context.Background()); err != nil {
		t.Fatalf("createMirrorEFIBootEntry() error = %v", err)
	}
	for _, d := range []string{"/dev/sda", "/dev/sdb"} {
		if !hasProvisionCall(cmd.calls, "chroot", newroot, "/bin/bash", "-c", "grub-install --target=i386-pc "+d) {
			t.Fatalf("missing BIOS grub-install for %s in %v", d, cmd.calls)
		}
	}
	want := []string{"copy /dev/sda1->/dev/sdb1", "mount /dev/sda1 " + bootEFIMountPoint()}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestCreateMirrorEFIBootEntryUEFIRequiresESP(t *testing.T) {
	origEFI := efiFirmwarePath
	t.Cleanup(func() { efiFirmwarePath = origEFI })
	efiFirmwarePath = t.TempDir()

	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, newTestDiskManager())
	o.targetDisk, o.mirrorDisk = "/dev/sda", "/dev/sdb"
	if err := o.createMirrorEFIBootEntry(context.Background()); err == nil {
		t.Fatal("createMirrorEFIBootEntry() error = nil for a UEFI install without ESP")
	}
}

func TestMirrorPairSurvivesResume(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.MirroringMode = "auto"
	o := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
	o.targetDisk, o.mirrorDisk = "/dev/sda", "/dev/sdb"
	cp := &Checkpoint{}
	o.recordCheckpointDerivedState("detect-disk", cp)
	if cp.MirrorTargetDisk != "/dev/sda" || cp.MirrorDisk != "/dev/sdb" {
		t.Fatalf("checkpoint mirror pair = %q, %q", cp.MirrorTargetDisk, cp.MirrorDisk)
	}

	tests := []struct {
		name    string
		missing string
		wantErr bool
	}{
		{name: "both disks present"},
		{name: "second disk missing", missing: "/dev/sdb", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMockStat(t, func(path string) (os.FileInfo, error) {
				if path == tt.missing {
					return nil, os.ErrNotExist
				}
				return fakeFileInfo{name: path, mode: os.ModeDevice}, nil
			})
			resumed := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
			resumed.restoreCheckpointDerivedState(cp)
			mirrored, err := resumed.selectMirrorPair(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("selectMirrorPair() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (!mirrored || resumed.targetDisk != "/dev/sda" || resumed.mirrorDisk != "/dev/sdb") {
				t.Fatalf("selectMirrorPair() = %v, %s, %s; want the checkpointed pair", mirrored, resumed.targetDisk, resumed.mirrorDisk)
			}
		})
	}
}

func TestSingleDiskFallbackSurvivesResume(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.MirroringMode = "auto"
	o := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
	o.targetDisk = "/dev/sda"
	cp := &Checkpoint{}
	o.recordCheckpointDerivedState("detect-disk", cp)
	if cp.MirrorTargetDisk != "/dev/sda" || cp.MirrorDisk != "" {
		t.Fatalf("checkpoint disk selection = %q, %q; want /dev/sda and no mirror", cp.MirrorTargetDisk, cp.MirrorDisk)
	}

	withMockStat(t, func(path string) (os.FileInfo, error) {
		return fakeFileInfo{name: path, mode: os.ModeDevice}, nil
	})
	resumed := NewOrchestrator(cfg, &mockProvider{}, newTestDiskManager())
	resumed.restoreCheckpointDerivedState(cp)
	chosen, err := resumed.selectMirrorPair(context.Background())
	if err != nil || !chosen || resumed.targetDisk != "/dev/sda" || resumed.mirrorDisk != "" {
		t.Fatalf("selectMirrorPair() = %v, %v, %s, %q; want the single disk from before resume",
			chosen, err, resumed.targetDisk, resumed.mirrorDisk)
	}
}

func TestSeparateBootPartition(t *testing.T) {
	tests := []struct {
		name     string
		parts    []disk.Partition
		wantBoot string
		wantErr  bool
	}{
		{name: "root, BIOS boot and ESP", parts: []disk.Partition{
			{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID},
			{Node: "/dev/sda14", Type: biosBootPartitionGUID},
			{Node: "/dev/sda15", Type: disk.EFISystemPartitionGUID},
		}},
		{name: "separate /boot partition", parts: []disk.Partition{
			{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID},
			{Node: "/dev/sda15", Type: disk.EFISystemPartitionGUID},
			{Node: "/dev/sda16", Type: xbootldrPartitionGUID},
		}, wantBoot: "/dev/sda16"},
		{name: "two extra Linux partitions", parts: []disk.Partition{
			{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID},
			{Node: "/dev/sda2", Type: disk.LinuxFilesystemGUID},
			{Node: "/dev/sda3", Type: disk.LinuxFilesystemGUID},
		}, wantErr: true},
		{name: "second ESP", parts: []disk.Partition{
			{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID},
			{Node: "/dev/sda15", Type: disk.EFISystemPartitionGUID},
			{Node: "/dev/sda16", Type: disk.EFISystemPartitionGUID},
		}, wantErr: true},
		{name: "swap partition", parts: []disk.Partition{
			{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID},
			{Node: "/dev/sda2", Type: "0657FD6D-A4AB-43C4-84E5-0933C84B4F4F"},
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, newTestDiskManager())
			o.rootPartition, o.bootPartition, o.targetPartitions = "/dev/sda1", "/dev/sda15", tt.parts
			boot, err := o.separateBootPartition(context.Background())
			if (err != nil) != tt.wantErr || boot != tt.wantBoot {
				t.Fatalf("separateBootPartition() = %q, %v; want %q, wantErr %v", boot, err, tt.wantBoot, tt.wantErr)
			}
		})
	}
}

func TestMirrorRootMirrorsSeparateBoot(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("mdadm --detail", nil, errors.New("no such array"))
	stubMirrorTailSpace(t, nil)
	var shrunk []string
	origShrink := shrinkFilesystemForMirrorFn
	shrinkFilesystemForMirrorFn = func(_ *disk.Manager, _ context.Context, member string) error {
		shrunk = append(shrunk, member)
		return nil
	}
	t.Cleanup(func() { shrinkFilesystemForMirrorFn = origShrink })

	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk, o.mirrorDisk = "/dev/sda", "/dev/sdb"
	o.rootPartition, o.bootPartition = "/dev/sda1", "/dev/sda15"
	o.targetPartitions = []disk.Partition{
		{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID},
		{Node: "/dev/sda15", Type: disk.EFISystemPartitionGUID},
		{Node: "/dev/sda16", Type: xbootldrPartitionGUID},
	}
	if err := o.mirrorRoot(context.Background()); err != nil {
		t.Fatalf("mirrorRoot() error = %v", err)
	}
	if len(shrunk) != 1 || shrunk[0] != "/dev/sda16" {
		t.Fatalf("shrunk filesystems = %v, want /dev/sda16", shrunk)
	}
	if strings.Join(o.mirrorArrays, ",") != disk.MirrorRootArray+","+disk.MirrorBootArray {
		t.Fatalf("mirrorArrays = %v", o.mirrorArrays)
	}
	for _, want := range [][]string{
		{"wipefs", "-af", "/dev/sdb16"},
		{"mdadm", "--create", "/dev/md1", "--run", "--level=1", "--raid-devices=2", "--metadata=1.0", "--assume-clean", "/dev/sda16", "missing"},
		{"mdadm", "--add", "/dev/md1", "/dev/sdb16"},
		{"mdadm", "--add", "/dev/md2", "/dev/sdb1"},
	} {
		if !hasProvisionCall(cmd.calls, want[0], want[1:]...) {
			t.Errorf("missing command %v", want)
		}
	}
}

func TestMdadmArrayLines(t *testing.T) {
	scan := "ARRAY /dev/md/2 metadata=1.0 name=host:2 UUID=a:b:c:d\n"
	got := mdadmArrayLines(disk.MirrorRootArray, scan)
	want := "ARRAY /dev/md2 metadata=1.0 name=host:2 UUID=a:b:c:d"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("mdadmArrayLines() = %q, want %q", got, want)
	}
}

func TestWriteMdadmConf(t *testing.T) {
	for _, shipsEtcConf := range []bool{false, true} {
		t.Run(fmt.Sprintf("image ships /etc/mdadm.conf=%v", shipsEtcConf), func(t *testing.T) {
			root := t.TempDir()
			alt := filepath.Join(root, "etc", "mdadm.conf")
			if shipsEtcConf {
				if err := os.MkdirAll(filepath.Dir(alt), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(alt, []byte("ARRAY /dev/md0 UUID=old\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			const content = "AUTO +imsm +1.x -all\nARRAY /dev/md2 UUID=new\n"
			if err := writeMdadmConf(root, content); err != nil {
				t.Fatalf("writeMdadmConf() error = %v", err)
			}
			wantPaths := []string{filepath.Join(root, "etc", "mdadm", "mdadm.conf")}
			if shipsEtcConf {
				wantPaths = append(wantPaths, alt)
			} else if fileExists(alt) {
				t.Fatalf("writeMdadmConf() created %s although the image has none", alt)
			}
			for _, path := range wantPaths {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != content {
					t.Fatalf("%s = %q, %v; want %q", path, data, err, content)
				}
			}
		})
	}
}

func TestMountMirrorBootMountsArrayBeforeESP(t *testing.T) {
	var mounts []string
	origMounted, origMount := isMountPoint, mountBootPart
	t.Cleanup(func() { isMountPoint, mountBootPart = origMounted, origMount })
	isMountPoint = func(string) bool { return false }
	mountBootPart = func(_ context.Context, _ *disk.Manager, device, mountpoint string) error {
		mounts = append(mounts, device+" "+mountpoint)
		return nil
	}

	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, newTestDiskManager())
	o.bootPartition = "/dev/sda15"
	o.mirrorArrays = []string{disk.MirrorRootArray, disk.MirrorBootArray}
	if err := o.mountBoot(context.Background()); err != nil {
		t.Fatalf("mountBoot() error = %v", err)
	}
	want := []string{disk.MirrorBootArray + " " + filepath.Join(newroot, "boot"), "/dev/sda15 " + bootEFIMountPoint()}
	if strings.Join(mounts, "|") != strings.Join(want, "|") {
		t.Fatalf("mounts = %v, want %v", mounts, want)
	}
}

func TestSeparateBootPartitionNeedsFstabForLinuxType(t *testing.T) {
	tests := []struct {
		name     string
		fstab    string
		wantBoot string
		wantErr  bool
	}{
		{name: "fstab mounts the partition at /boot by label", fstab: "LABEL=root / ext4 defaults 0 1\nLABEL=BOOT /boot ext4 defaults 0 2\n", wantBoot: "/dev/sda2"},
		{name: "fstab mounts the partition at /var", fstab: "LABEL=root / ext4 defaults 0 1\nLABEL=BOOT /var ext4 defaults 0 2\n", wantErr: true},
		{name: "fstab mounts another /boot", fstab: "UUID=other /boot ext4 defaults 0 2\n", wantErr: true},
		{name: "fstab mounts the partition at /boot by partition label", fstab: "PARTLABEL=boot /boot ext4 defaults 0 2\n", wantBoot: "/dev/sda2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origMount, origUnmount := mountReadOnlyPart, unmountInspectionPart
			t.Cleanup(func() { mountReadOnlyPart, unmountInspectionPart = origMount, origUnmount })
			unmountInspectionPart = func(*disk.Manager, string) error { return nil }
			mountReadOnlyPart = func(_ context.Context, _ *disk.Manager, _, dir string) error {
				if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(dir, "etc", "fstab"), []byte(tt.fstab), 0o644)
			}
			cmd := newMockCommander()
			cmd.setResult("mdadm --detail", nil, errors.New("no such array"))
			cmd.setResult("blkid -o", []byte("DEVNAME=/dev/sda2\nLABEL=BOOT\nUUID=1234\nTYPE=ext4\nPARTLABEL=boot\n"), nil)
			o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
			o.targetDisk, o.rootPartition = "/dev/sda", "/dev/sda1"
			o.targetPartitions = []disk.Partition{
				{Node: "/dev/sda1", Type: disk.LinuxFilesystemGUID, Name: "root"},
				{Node: "/dev/sda2", Type: disk.LinuxFilesystemGUID},
			}
			boot, err := o.separateBootPartition(context.Background())
			if (err != nil) != tt.wantErr || boot != tt.wantBoot {
				t.Fatalf("separateBootPartition() = %q, %v; want %q, wantErr %v", boot, err, tt.wantBoot, tt.wantErr)
			}
		})
	}
}

func TestRewriteMirrorFstab(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	fstab := "# static\nPARTUUID=aaa / ext4 defaults 0 1\nPARTLABEL=boot /boot ext4 defaults 0 2\n" +
		"/dev/sda15 /boot/efi vfat umask=0077 0 1\nLABEL=data /data ext4 defaults 0 2\n"
	if err := os.WriteFile(filepath.Join(root, "etc", "fstab"), []byte(fstab), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newMockCommander()
	cmd.setResult("blkid -s", []byte("ABCD-1234\n"), nil)
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.bootPartition = "/dev/sda15"
	o.mirrorArrays = []string{disk.MirrorRootArray, disk.MirrorBootArray}
	if err := o.rewriteMirrorFstab(context.Background(), root); err != nil {
		t.Fatalf("rewriteMirrorFstab() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "etc", "fstab"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# static\n/dev/md2 / ext4 defaults 0 1\n/dev/md1 /boot ext4 defaults 0 2\n" +
		"UUID=ABCD-1234 /boot/efi vfat umask=0077 0 1\nLABEL=data /data ext4 defaults 0 2\n"
	if string(got) != want {
		t.Fatalf("fstab =\n%s\nwant\n%s", got, want)
	}
}

func TestRewriteMirrorFstabAddsBootEntry(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "fstab"), []byte("LABEL=root / ext4 defaults 0 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newMockCommander()
	cmd.setResult("blkid -o", []byte("ext4\n"), nil)
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.mirrorArrays = []string{disk.MirrorRootArray, disk.MirrorBootArray}
	if err := o.rewriteMirrorFstab(context.Background(), root); err != nil {
		t.Fatalf("rewriteMirrorFstab() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "etc", "fstab"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "LABEL=root / ext4 defaults 0 1\n/dev/md1 /boot ext4 defaults 0 2\n"; string(got) != want {
		t.Fatalf("fstab = %q, want %q", got, want)
	}
}

func TestRewriteMirrorFstabWithoutFstab(t *testing.T) {
	root := t.TempDir()
	cmd := newMockCommander()
	cmd.setResult("blkid -o", []byte("ext4\n"), nil)
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.mirrorArrays = []string{disk.MirrorRootArray, disk.MirrorBootArray}
	if err := o.rewriteMirrorFstab(context.Background(), root); err != nil {
		t.Fatalf("rewriteMirrorFstab() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "etc", "fstab"))
	if err != nil || string(got) != "/dev/md1 /boot ext4 defaults 0 2\n" {
		t.Fatalf("fstab = %q, %v; want the /boot array entry", got, err)
	}
}

func TestReadTargetFstabStaysInRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(outside, []byte("host /boot ext4 defaults 0 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "etc", "fstab")); err != nil {
		t.Fatal(err)
	}
	if _, err := fstabEntrySource(root, "/boot"); err == nil {
		t.Fatal("fstabEntrySource() followed an fstab symlink out of the target root")
	}
}

func TestRewriteMirrorFstabFailsWithoutESPUUID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "fstab"), []byte("PARTUUID=ccc /boot/efi vfat umask=0077 0 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newMockCommander()
	cmd.setResult("blkid -s", nil, nil)
	o := NewOrchestrator(&config.MachineConfig{}, &mockProvider{}, disk.NewManager(cmd))
	o.bootPartition = "/dev/sda15"
	o.mirrorArrays = []string{disk.MirrorRootArray}
	if err := o.rewriteMirrorFstab(context.Background(), root); err == nil {
		t.Fatal("rewriteMirrorFstab() error = nil, want an error when the ESP UUID is unknown")
	}
}
