//go:build linux

package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDiskLinks(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("blkid -c", []byte(`DEVNAME=/dev/sda1
UUID=7B77-95E7
BLOCK_SIZE=512
TYPE=vfat
PARTLABEL=EFI\ System\ Partition
PARTUUID=0f2c3a5e-01

DEVNAME=/dev/sda2
LABEL=cloudimg-rootfs
UUID=5d7a1d2b-1111-2222-3333-444455556666
TYPE=ext4
PARTUUID=0f2c3a5e-02

DEVNAME=/dev/nvme0n1p2
LABEL=data/a b
UUID=5d7a1d2b-1111-2222-3333-444455556666
TYPE=ext4
`), nil)
	devDir := t.TempDir()
	// A stale link from an earlier run is replaced.
	stale := filepath.Join(devDir, "disk", "by-uuid", "dead-beef")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../sdz9", stale); err != nil {
		t.Fatal(err)
	}

	m := NewManager(cmd)
	if err := m.CreateDiskLinks(context.Background(), devDir); err != nil {
		t.Fatalf("CreateDiskLinks() = %v", err)
	}
	want := map[string]string{
		"by-uuid/7B77-95E7":                            "../../sda1",
		"by-uuid/5d7a1d2b-1111-2222-3333-444455556666": "../../sda2", // first device wins
		"by-partuuid/0f2c3a5e-01":                      "../../sda1",
		"by-partuuid/0f2c3a5e-02":                      "../../sda2",
		"by-label/cloudimg-rootfs":                     "../../sda2",
		`by-label/data\x2fa\x20b`:                      "../../nvme0n1p2",
		`by-partlabel/EFI\x20System\x20Partition`:      "../../sda1",
	}
	for rel, target := range want {
		got, err := os.Readlink(filepath.Join(devDir, "disk", rel))
		if err != nil || got != target {
			t.Errorf("link %s -> %q (%v), want %q", rel, got, err, target)
		}
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Errorf("stale link %s still exists", stale)
	}
	if !hasDiskCommandCall(cmd.calls, "blkid", "-c", "/dev/null", "-o", "export") {
		t.Fatalf("blkid not called with the cache disabled: %v", cmd.calls)
	}
}

func TestCreateDiskLinksBlkidWithoutDevices(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("blkid -c", nil, makeExitError(2))
	if err := NewManager(cmd).CreateDiskLinks(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("CreateDiskLinks() = %v, want nil when blkid finds no device", err)
	}
}
