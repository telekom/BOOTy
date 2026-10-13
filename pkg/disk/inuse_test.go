//go:build linux

package disk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inUseFixture builds a sysfs and procfs tree: sda2 is an LVM PV whose LV
// dm-0 is mounted at /, sdc1 is an active swap, sdb is free.
func inUseFixture(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	sys, proc := filepath.Join(root, "sys"), filepath.Join(root, "proc")
	mk := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(sys, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(sys, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(sys, path)); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"devices/sda/sda1", "devices/sda/sda2", "devices/sdb", "devices/sdc/sdc1", "devices/dm-0/slaves/sda2",
		"devices/sdd/sdd1", "devices/sde/sde1", "devices/md0/slaves/sdd1", "devices/md0/slaves/sde1",
		"devices/sdf/sdf1", "devices/sdg", "devices/sdh", "devices/sdi/sdi1",
		"fs/btrfs/4f2c/devices/sdg", "fs/btrfs/4f2c/devices/sdh"} {
		mk(d)
	}
	for _, p := range []string{"devices/sda/sda1", "devices/sda/sda2", "devices/sdc/sdc1", "devices/sdd/sdd1", "devices/sde/sde1", "devices/sdf/sdf1", "devices/sdi/sdi1"} {
		write(filepath.Join(sys, p, "partition"), "1\n")
	}
	for name, dev := range map[string]string{"sda": "sda", "sda1": "sda/sda1", "sda2": "sda/sda2", "sdb": "sdb", "sdc": "sdc", "sdc1": "sdc/sdc1", "dm-0": "dm-0",
		"sdd": "sdd", "sdd1": "sdd/sdd1", "sde": "sde", "sde1": "sde/sde1", "md0": "md0",
		"sdf": "sdf", "sdf1": "sdf/sdf1", "sdg": "sdg", "sdh": "sdh", "sdi": "sdi", "sdi1": "sdi/sdi1"} {
		link("../../devices/"+dev, "class/block/"+name)
	}
	for _, disk := range []string{"sda", "sdb", "sdc", "dm-0"} {
		link("../devices/"+disk, "block/"+disk)
	}
	link("../../devices/dm-0", "dev/block/253:0")
	link("../../devices/sda/sda1", "dev/block/8:1")
	link("../../devices/md0", "dev/block/9:0")
	write(filepath.Join(proc, "self", "mountinfo"),
		"22 1 253:0 / / rw,relatime shared:1 - ext4 /dev/mapper/vg-root rw\n"+
			"23 22 0:21 / /run rw shared:2 - tmpfs tmpfs rw\n"+
			"24 22 9:0 / /data rw - ext4 /dev/md0 rw\n"+
			// btrfs reports an anonymous device number; the source names sdf1.
			"40 22 0:36 /root /srv rw - btrfs /dev/sdf1 rw\n"+
			// Multi-device btrfs mounted by its first device sdg.
			"41 22 0:37 / /pool rw - btrfs /dev/sdg rw\n"+
			// Escaped mount point and source.
			`42 22 0:38 / /mnt/my\040disk rw - ext4 /dev/sdi1 rw`+"\n")
	write(filepath.Join(proc, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n/dev/sdc1\tpartition\t1024\t0\t-2\n")

	m := newManagerWithSysfs(guardedCommander{newMockCommander()}, sys)
	m.procDir = proc
	return m
}

// guardedCommander records commands like mockCommander but is not a
// FakeDisks commander, so refuseInUse runs.
type guardedCommander struct{ mock *mockCommander }

func (g guardedCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return g.mock.Run(ctx, name, args...)
}

func TestRefuseInUse(t *testing.T) {
	m := inUseFixture(t)
	for device, want := range map[string]string{
		"/dev/sda":  "mounted at /",
		"/dev/sda2": "mounted at /",
		"/dev/dm-0": "mounted at /",
		"/dev/sdc":  "active swap /dev/sdc1",
		"/dev/sdc1": "active swap /dev/sdc1",
		"/dev/sdb":  "",
		"/dev/sda1": "",                 // not mounted, but its disk is busy
		"/dev/sdd":  "mounted at /data", // MD member
		"/dev/sde":  "mounted at /data",
		"/dev/sdf":  "mounted at /srv",  // btrfs, anonymous device number
		"/dev/sdh":  "mounted at /pool", // second device of a multi-device btrfs
		"/dev/sdi":  "mounted at /mnt/my disk",
	} {
		err := m.refuseInUse(device)
		if want == "" {
			if err != nil {
				t.Errorf("refuseInUse(%s) = %v, want nil", device, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("refuseInUse(%s) = %v, want %q", device, err, want)
		}
	}
}

// A busy disk is not touched: WipeDisk and the secure erase fail before any
// command, and WipeAllDisks wipes only the free disk and reports the others.
func TestWipesSkipDisksInUse(t *testing.T) {
	m := inUseFixture(t)
	cmd := m.cmd.(guardedCommander).mock
	if err := m.WipeDisk(context.Background(), "/dev/sda"); err == nil || !strings.Contains(err.Error(), "refusing to wipe /dev/sda") {
		t.Fatalf("WipeDisk(/dev/sda) = %v", err)
	}
	if err := m.SecureEraseDisk(context.Background(), "/dev/sdc"); err == nil {
		t.Fatal("SecureEraseDisk(/dev/sdc) = nil, want the in-use error")
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("commands ran on busy disks: %v", callList(cmd.calls))
	}
	err := m.WipeAllDisks(context.Background())
	if err == nil || !strings.Contains(err.Error(), "wiped 1 disk(s) but 2 failed") {
		t.Fatalf("WipeAllDisks() = %v", err)
	}
	for _, c := range callList(cmd.calls) {
		if !strings.HasSuffix(c, "/dev/sdb") && !strings.HasPrefix(c, "lsblk") {
			t.Fatalf("WipeAllDisks ran %q on a busy disk", c)
		}
	}
}

// Without mountinfo or sysfs BOOTy can not tell, so it does not wipe.
func TestRefuseInUseFailsClosed(t *testing.T) {
	m := inUseFixture(t)
	m.procDir = t.TempDir()
	if err := m.refuseInUse("/dev/sdb"); err == nil || !strings.Contains(err.Error(), "cannot check") {
		t.Fatalf("refuseInUse() without mountinfo = %v, want an error", err)
	}
	m = inUseFixture(t)
	m.sysfsRoot = t.TempDir()
	if err := m.refuseInUse("/dev/sdb"); err == nil || !strings.Contains(err.Error(), "cannot check") {
		t.Fatalf("refuseInUse() without sysfs = %v, want an error", err)
	}
}

// Every commander except a FakeDisks test double gets the check.
func TestRefuseInUseRunsForWrappedCommanders(t *testing.T) {
	m := NewManager(guardedCommander{newMockCommander()})
	m.procDir = t.TempDir()
	if err := m.WipeDisk(context.Background(), "/dev/sdb"); err == nil || !strings.Contains(err.Error(), "cannot check") {
		t.Fatalf("WipeDisk() = %v, want the in-use check to run", err)
	}
}
