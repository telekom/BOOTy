//go:build linux

package disk

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
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

func TestDetectMirrorPairSkipsNamespacesOfOneController(t *testing.T) {
	tests := []struct {
		name             string
		disks            []string
		wantSecond       string
		perNamespaceLink bool
	}{
		{name: "namespaces of one controller only", disks: []string{"nvme0n1", "nvme0n2"}},
		{name: "second controller available", disks: []string{"nvme0n1", "nvme0n2", "nvme1n1"}, wantSecond: "/dev/nvme1n1"},
		{name: "namespace device links differ", disks: []string{"nvme0n1", "nvme0n2"}, perNamespaceLink: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sysfs := t.TempDir()
			for _, name := range tt.disks {
				writeSysfsBlockDevice(t, sysfs, name, "0", 20, "")
				ctrl := filepath.Join(sysfs, "ctrl", name[:len("nvme0")])
				if tt.perNamespaceLink {
					// Some kernels link each namespace to its own sysfs node.
					ctrl = filepath.Join(ctrl, name)
				}
				if err := os.MkdirAll(ctrl, 0o755); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(sysfs, "block", name, "device")
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(ctrl, link); err != nil {
					t.Fatal(err)
				}
			}
			first, second, ok, err := newManagerWithSysfs(newMockCommander(), sysfs).DetectMirrorPair(context.Background(), 0)
			if err != nil {
				t.Fatalf("DetectMirrorPair() error = %v", err)
			}
			if ok != (tt.wantSecond != "") || second != tt.wantSecond {
				t.Fatalf("DetectMirrorPair() = %s, %s, %v; want second %q", first, second, ok, tt.wantSecond)
			}
		})
	}
}

func TestMirrorHasMember(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("mdadm --detail", []byte("   0  259  2  0  active sync  /dev/nvme0n1p2\n"), nil)
	m := NewManager(cmd)
	for member, want := range map[string]bool{"/dev/nvme0n1p2": true, "/dev/nvme1n1p2": false, "/dev/nvme0n1p": false} {
		got, err := m.MirrorHasMember(context.Background(), MirrorRootArray, member)
		if err != nil || got != want {
			t.Errorf("MirrorHasMember(%s) = %v, %v; want %v", member, got, err, want)
		}
	}
}

func writeSuperblockImage(t *testing.T, size int64, offset int64, sb []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "part.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(sb, offset); err != nil {
		t.Fatal(err)
	}
	return path
}

func extSuperblock(blocks uint64, logBlockSize uint32) []byte {
	sb := make([]byte, 1024)
	binary.LittleEndian.PutUint32(sb[4:], uint32(blocks))
	binary.LittleEndian.PutUint32(sb[24:], logBlockSize)
	binary.LittleEndian.PutUint16(sb[56:], 0xEF53)
	if blocks>>32 != 0 {
		binary.LittleEndian.PutUint32(sb[96:], 0x80)
		binary.LittleEndian.PutUint32(sb[0x150:], uint32(blocks>>32))
	}
	return sb
}

func TestCheckMirrorTailSpace(t *testing.T) {
	const mib = 1 << 20
	xfs := make([]byte, 16)
	copy(xfs, "XFSB")
	binary.BigEndian.PutUint32(xfs[4:], 4096)
	binary.BigEndian.PutUint64(xfs[8:], 64*mib/4096)
	btrfs := make([]byte, 0x78)
	copy(btrfs[0x40:], "_BHRfS_M")
	binary.LittleEndian.PutUint64(btrfs[0x70:], 64*mib)

	tests := []struct {
		name    string
		size    int64
		offset  int64
		sb      []byte
		wantErr bool
	}{
		{name: "ext4 filesystem with grown partition", size: 128 * mib, offset: 1024, sb: extSuperblock(64*mib/4096, 2)},
		{name: "ext4 filesystem fills partition", size: 64 * mib, offset: 1024, sb: extSuperblock(64*mib/4096, 2), wantErr: true},
		{name: "ext4 filesystem within metadata reserve", size: 64*mib + 512*1024, offset: 1024, sb: extSuperblock(64*mib/4096, 2), wantErr: true},
		{name: "ext4 64bit block count", size: 128 * mib, offset: 1024, sb: extSuperblock(1<<32, 2), wantErr: true},
		{name: "xfs filesystem with grown partition", size: 128 * mib, offset: 0, sb: xfs},
		{name: "xfs filesystem fills partition", size: 64 * mib, offset: 0, sb: xfs, wantErr: true},
		{name: "btrfs filesystem with grown partition", size: 128 * mib, offset: 0x10000, sb: btrfs},
		{name: "unknown filesystem", size: 128 * mib, offset: 0, sb: []byte("nothing"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSuperblockImage(t, tt.size, tt.offset, tt.sb)
			if err := CheckMirrorTailSpace(path); (err != nil) != tt.wantErr {
				t.Fatalf("CheckMirrorTailSpace() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
