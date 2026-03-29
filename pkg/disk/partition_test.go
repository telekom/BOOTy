//go:build linux

package disk

import (
	"fmt"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestParsePartitionLayout(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		parts   int
		table   string
	}{
		{
			name:    "valid layout",
			input:   `{"table":"gpt","partitions":[{"label":"efi","sizeMB":512,"filesystem":"vfat","mountpoint":"/boot/efi"},{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`,
			wantErr: false,
			parts:   2,
			table:   "gpt",
		},
		{
			name:    "default table type",
			input:   `{"partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`,
			wantErr: false,
			parts:   1,
			table:   "gpt",
		},
		{
			name:    "empty partitions",
			input:   `{"partitions":[]}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `{invalid}`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			layout, err := config.ParsePartitionLayout(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(layout.Partitions) != tc.parts {
				t.Errorf("got %d partitions, want %d", len(layout.Partitions), tc.parts)
			}
			if tc.table != "" && layout.Table != tc.table {
				t.Errorf("got table %q, want %q", layout.Table, tc.table)
			}
			if layout.Table == "" {
				t.Error("expected default table to be set, got empty")
			}
		})
	}
}

func TestResolveTypeGUID(t *testing.T) {
	tests := []struct {
		name string
		part config.Partition
		want string
	}{
		{"explicit GUID", config.Partition{TypeGUID: "custom-guid"}, "custom-guid"},
		{"vfat → EFI", config.Partition{Filesystem: "vfat"}, EFISystemPartitionGUID},
		{"swap", config.Partition{Filesystem: "swap"}, "0657FD6D-A4AB-43C4-84E5-0933C84B4F4F"},
		{"ext4 → linux", config.Partition{Filesystem: "ext4"}, LinuxFilesystemGUID},
		{"default → linux", config.Partition{}, LinuxFilesystemGUID},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTypeGUID(tc.part)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPartitionDevice(t *testing.T) {
	tests := []struct {
		device string
		num    int
		want   string
	}{
		{"/dev/sda", 1, "/dev/sda1"},
		{"/dev/sda", 2, "/dev/sda2"},
		{"/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"/dev/nvme0n1", 3, "/dev/nvme0n1p3"},
		{"/dev/loop0", 1, "/dev/loop0p1"},
		{"/dev/mmcblk0", 1, "/dev/mmcblk0p1"},
		{"/dev/md0", 1, "/dev/md0p1"},
		{"/dev/nbd0", 1, "/dev/nbd0p1"},
	}
	for _, tc := range tests {
		t.Run(tc.device, func(t *testing.T) {
			got := partitionDevice(tc.device, tc.num)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestGenerateFstab(t *testing.T) {
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "efi", SizeMB: 512, Filesystem: "vfat", Mountpoint: "/boot/efi"},
			{Label: "root", Filesystem: "ext4", Mountpoint: "/"},
		},
	}

	fstab := GenerateFstab(layout, "/dev/sda")
	if fstab == "" {
		t.Fatal("fstab is empty")
	}
	if !strings.Contains(fstab, "PARTLABEL=efi") || !strings.Contains(fstab, "/boot/efi") {
		t.Errorf("fstab missing EFI entry:\n%s", fstab)
	}
	if !strings.Contains(fstab, "PARTLABEL=root") || !strings.Contains(fstab, "ext4") {
		t.Errorf("fstab missing root entry:\n%s", fstab)
	}
}

func TestGenerateFstabEscapesPartlabel(t *testing.T) {
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root data", Filesystem: "ext4", Mountpoint: "/"},
		},
	}

	fstab := GenerateFstab(layout, "/dev/sda")
	if !strings.Contains(fstab, "PARTLABEL=root\\040data") {
		t.Errorf("fstab missing escaped PARTLABEL entry:\n%s", fstab)
	}
}

func TestEscapeFstabValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"root", "root"},
		{"root data", "root\\040data"},
		{"back\\slash", "back\\\\slash"},
		{"hash#value", "hash\\043value"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := escapeFstabValue(tc.input)
			if got != tc.want {
				t.Errorf("escapeFstabValue(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestGenerateFstabNil(t *testing.T) {
	got := GenerateFstab(nil, "/dev/sda")
	if got != "" {
		t.Errorf("GenerateFstab(nil) = %q, want empty", got)
	}
}

func TestApplyPartitionLayoutNilLayout(t *testing.T) {
	mgr := &Manager{}
	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", nil)
	if err == nil {
		t.Error("expected error for nil layout")
	}
}

func TestApplyPartitionLayoutUnsupportedTable(t *testing.T) {
	mgr := &Manager{}
	layout := &config.PartitionLayout{
		Table:      "mbr",
		Partitions: []config.Partition{{Label: "test"}},
	}
	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Error("expected error for mbr table type")
	}
}

func TestApplyPartitionLayoutEmptyDevice(t *testing.T) {
	mgr := &Manager{}
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "root"}},
	}
	err := mgr.ApplyPartitionLayout(t.Context(), "", layout)
	if err == nil {
		t.Error("expected error for empty device")
	}
}

func TestApplyPartitionLayoutSkipsFormattingLvmPVPartition(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "pv", SizeMB: 8192},
			{Label: "root", Filesystem: "ext4", Mountpoint: "/"},
		},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
		},
	}

	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	formattedPV := false
	formattedRoot := false
	for _, call := range cmd.calls {
		if call.name != "mkfs.ext4" || len(call.args) == 0 {
			continue
		}
		target := call.args[len(call.args)-1]
		if target == "/dev/sda1" {
			formattedPV = true
		}
		if target == "/dev/sda2" {
			formattedRoot = true
		}
	}

	if formattedPV {
		t.Fatal("expected LVM PV partition to be skipped for formatting")
	}
	if !formattedRoot {
		t.Fatal("expected non-PV partition with filesystem to be formatted")
	}
}

func TestApplyPartitionLayoutSetsLVMTypeGUID(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "pv", SizeMB: 8192},
			{Label: "root", Filesystem: "ext4", Mountpoint: "/"},
		},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
		},
	}

	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedArg := "--typecode=1:" + LinuxLVMGUID
	found := false
	for _, call := range cmd.calls {
		if call.name != "sgdisk" {
			continue
		}
		for _, arg := range call.args {
			if arg == expectedArg {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected sgdisk called with %s for LVM PV partition", expectedArg)
	}
}

func TestParsePartitionLayout_MissingLabel(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"sizeMB":512,"filesystem":"vfat"}]}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for missing label")
	}
}

func TestParsePartitionLayout_RelativeMountpoint(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":512,"filesystem":"ext4","mountpoint":"boot"}]}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for relative mountpoint")
	}
}

func TestParsePartitionLayout_NegativeSize(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":-100,"filesystem":"ext4","mountpoint":"/"}]}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for negative sizeMB")
	}
}

func TestParsePartitionLayout_InvalidLVMName(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}],"lvm":{"volumeGroup":"sys/vg","pvPartition":1,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for invalid LVM VG name")
	}
}

func TestParsePartitionLayout_NoRootMountpoint(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"data","sizeMB":1024,"filesystem":"ext4","mountpoint":"/data"}]}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for missing root mountpoint")
	}
}

func TestParsePartitionLayout_MultipleFillRemaining(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"efi","sizeMB":512,"filesystem":"vfat","mountpoint":"/boot/efi"},{"label":"root","filesystem":"ext4","mountpoint":"/"},{"label":"data","filesystem":"ext4","mountpoint":"/data"}]}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for multiple sizeMB=0 partitions")
	}
}

func TestParsePartitionLayout_FillRemainingNotLast(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"},{"label":"data","sizeMB":1024,"filesystem":"xfs","mountpoint":"/data"}]}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error when sizeMB=0 is not the last partition")
	}
}

func TestParsePartitionLayout_InvalidPVPartition(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":1024,"filesystem":"ext4","mountpoint":"/"}],"lvm":{"volumeGroup":"sysvg","pvPartition":0,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for pvPartition=0")
	}
}

func TestGenerateFstabSwap(t *testing.T) {
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", SizeMB: 8192, Filesystem: "ext4", Mountpoint: "/"},
			{Label: "swap", SizeMB: 2048, Filesystem: "swap"},
		},
	}
	fstab := GenerateFstab(layout, "/dev/sda")
	if !strings.Contains(fstab, "none\tswap\tsw") {
		t.Errorf("fstab missing swap entry:\n%s", fstab)
	}
	if !strings.Contains(fstab, "PARTLABEL=swap") {
		t.Errorf("fstab missing swap device:\n%s", fstab)
	}
}

func TestPartitionDevicePath(t *testing.T) {
	tests := []struct {
		device string
		num    int
		want   string
	}{
		{"/dev/sda", 1, "/dev/sda1"},
		{"/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"/tmp/nvme-dir/sda", 1, "/tmp/nvme-dir/sda1"},
		{"/dev/md0", 1, "/dev/md0p1"},
		{"/dev/nbd0", 2, "/dev/nbd0p2"},
		{"/dev/disk/by-id/nvme-eui.0011223344556677", 1, "/dev/disk/by-id/nvme-eui.0011223344556677-part1"},
		{"/dev/disk/by-path/pci-0000:00:1f.2-ata-1", 2, "/dev/disk/by-path/pci-0000:00:1f.2-ata-1-part2"},
	}
	for _, tc := range tests {
		got := PartitionDevicePath(tc.device, tc.num)
		if got != tc.want {
			t.Errorf("PartitionDevicePath(%s, %d) = %s, want %s", tc.device, tc.num, got, tc.want)
		}
	}
}

func TestParsePartitionLayout_EmptyLVMVGName(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}],"lvm":{"volumeGroup":"","pvPartition":1,"volumes":[{"name":"root"}]}}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for empty volumeGroup")
	}
}

func TestParsePartitionLayout_RelativeLVMMountpoint(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","filesystem":"ext4","mountpoint":"/"}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"data","mountpoint":"data"}]}}`
	_, err := config.ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for relative LVM mountpoint")
	}
}

func TestGenerateLVMFstab(t *testing.T) {
	lvm := &config.LVMConfig{
		VolumeGroup: "sysvg",
		Volumes: []config.LVVolume{
			{Name: "root", Filesystem: "ext4", Mountpoint: "/"},
			{Name: "var", Filesystem: "xfs", Mountpoint: "/var"},
			{Name: "swap", Filesystem: "swap"},
		},
	}
	fstab := GenerateLVMFstab(lvm)
	if fstab == "" {
		t.Fatal("fstab is empty")
	}
	if !strings.Contains(fstab, "/dev/sysvg/root") {
		t.Errorf("fstab missing root LV:\n%s", fstab)
	}
	if !strings.Contains(fstab, "/dev/sysvg/var") {
		t.Errorf("fstab missing var LV:\n%s", fstab)
	}
	if !strings.Contains(fstab, "/dev/sysvg/swap\tnone\tswap\tsw") {
		t.Errorf("fstab missing swap LV entry:\n%s", fstab)
	}
}

func TestGenerateLVMFstabNil(t *testing.T) {
	got := GenerateLVMFstab(nil)
	if got != "" {
		t.Errorf("GenerateLVMFstab(nil) = %q, want empty", got)
	}
}

func TestApplyLVMConfig_NilLayout(t *testing.T) {
	mgr := &Manager{}
	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", nil)
	if err != nil {
		t.Errorf("expected nil error for nil layout, got %v", err)
	}
}

func TestApplyLVMConfig_InvalidPVPartition(t *testing.T) {
	mgr := &Manager{}
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "root"}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 0,
			Volumes:     []config.LVVolume{{Name: "root"}},
		},
	}
	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Error("expected error for PVPartition < 1")
	}
}

func TestApplyLVMConfig_EmptyDevice(t *testing.T) {
	mgr := &Manager{}
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "root", Filesystem: "ext4", Mountpoint: "/"}},
		},
	}
	err := mgr.ApplyLVMConfig(t.Context(), "", layout)
	if err == nil {
		t.Error("expected error for empty device")
	}
}

func TestApplyLVMConfig_PVPartitionExceedsCount(t *testing.T) {
	mgr := &Manager{}
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "root"}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 5,
			Volumes:     []config.LVVolume{{Name: "root"}},
		},
	}
	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Error("expected error for PVPartition exceeding partition count")
	}
}

func TestApplyPartitionLayoutEmptyPartitions(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{},
	}
	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Error("expected error for empty partitions")
	}
}

func TestApplyPartitionLayoutCommandSequence(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "efi", SizeMB: 512, Filesystem: "vfat", Mountpoint: "/boot/efi"},
			{Label: "root", Filesystem: "ext4", Mountpoint: "/"},
		},
	}

	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify command sequence: sgdisk --zap-all, sgdisk --new (x2), partprobe, mkfs.vfat, mkfs.ext4.
	expected := []struct {
		name   string
		argSub string // substring that must appear in any arg
	}{
		{"sgdisk", "--zap-all"},
		{"sgdisk", "--new=1:"},
		{"sgdisk", "--new=2:"},
		{"partprobe", "/dev/sda"},
		{"mkfs.vfat", "/dev/sda1"},
		{"mkfs.ext4", "/dev/sda2"},
	}

	if len(cmd.calls) < len(expected) {
		t.Fatalf("expected at least %d commands, got %d", len(expected), len(cmd.calls))
	}

	idx := 0
	for _, call := range cmd.calls {
		if idx >= len(expected) {
			break
		}
		exp := expected[idx]
		if call.name != exp.name {
			continue
		}
		found := false
		// Check command name match with arg substring.
		allArgs := strings.Join(call.args, " ")
		if strings.Contains(allArgs, exp.argSub) || exp.argSub == "" {
			found = true
		}
		if found {
			idx++
		}
	}
	if idx != len(expected) {
		got := make([]string, 0, len(cmd.calls))
		for _, c := range cmd.calls {
			got = append(got, c.name+" "+strings.Join(c.args, " "))
		}
		t.Errorf("command sequence mismatch, matched %d/%d expected commands.\nGot calls:\n%s",
			idx, len(expected), strings.Join(got, "\n"))
	}
}

func TestApplyPartitionLayoutZapError(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("sgdisk --zap-all", []byte("zap error output"), fmt.Errorf("sgdisk failed"))
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", Filesystem: "ext4", Mountpoint: "/"},
		},
	}

	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error when sgdisk --zap-all fails")
	}
	if !strings.Contains(err.Error(), "zapping partition table") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyPartitionLayoutPartprobeError(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("partprobe /dev/sda", nil, fmt.Errorf("partprobe failed"))
	cmd.setResult("blockdev --rereadpt", nil, fmt.Errorf("blockdev rereadpt failed"))
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", SizeMB: 8192, Filesystem: "ext4", Mountpoint: "/"},
		},
	}

	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error when partprobe and blockdev --rereadpt both fail")
	}
	if !strings.Contains(err.Error(), "re-read partition table") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyPartitionLayoutFormatError(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("mkfs.ext4 -F", []byte("format error"), fmt.Errorf("mkfs failed"))
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", SizeMB: 8192, Filesystem: "ext4", Mountpoint: "/"},
		},
	}

	err := mgr.ApplyPartitionLayout(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error when mkfs fails")
	}
	if !strings.Contains(err.Error(), "formatting partition") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyLVMConfigCommandSequence(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "pv", SizeMB: 8192},
			{Label: "efi", SizeMB: 512, Filesystem: "vfat", Mountpoint: "/boot/efi"},
		},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes: []config.LVVolume{
				{Name: "root", SizeMB: 4096, Filesystem: "ext4", Mountpoint: "/"},
				{Name: "var", Extents: "100%FREE", Filesystem: "xfs", Mountpoint: "/var"},
			},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify: pvcreate, vgcreate, and per-LV create+format ordering.
	expected := []string{"pvcreate", "vgcreate", "lvcreate", "mkfs.ext4", "lvcreate", "mkfs.xfs"}
	idx := 0
	for _, call := range cmd.calls {
		if idx >= len(expected) {
			break
		}
		if call.name == expected[idx] {
			idx++
		}
	}
	if idx != len(expected) {
		got := make([]string, 0, len(cmd.calls))
		for _, c := range cmd.calls {
			got = append(got, c.name+" "+strings.Join(c.args, " "))
		}
		t.Errorf("expected commands %v, matched %d/%d.\nGot:\n%s",
			expected, idx, len(expected), strings.Join(got, "\n"))
	}
}

func TestApplyLVMConfigPvcreateError(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("pvcreate -f", []byte("pv error"), fmt.Errorf("pvcreate failed"))
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "root", Filesystem: "ext4", Mountpoint: "/"}},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error when pvcreate fails")
	}
	if !strings.Contains(err.Error(), "pvcreate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyLVMConfigVgcreateError(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("vgcreate sysvg", []byte("vg error"), fmt.Errorf("vgcreate failed"))
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "root", Filesystem: "ext4", Mountpoint: "/"}},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error when vgcreate fails")
	}
	if !strings.Contains(err.Error(), "vgcreate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyLVMConfigLvcreateError(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("lvcreate -L", []byte("lv error"), fmt.Errorf("lvcreate failed"))
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "root", SizeMB: 4096, Filesystem: "ext4", Mountpoint: "/"}},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error when lvcreate fails")
	}
	if !strings.Contains(err.Error(), "lvcreate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyLVMConfigRejectsUnsafeVGName(t *testing.T) {
	mgr := NewManager(newMockCommander())
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "-sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "root", Filesystem: "ext4", Mountpoint: "/"}},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error for unsafe volume group name")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyLVMConfigRejectsUnsafeLVName(t *testing.T) {
	mgr := NewManager(newMockCommander())
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "-root", Filesystem: "ext4", Mountpoint: "/"}},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err == nil {
		t.Fatal("expected error for unsafe logical volume name")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyLVMConfigVGAlreadyExists(t *testing.T) {
	cmd := newMockCommander()
	// vgs succeeds → VG already exists, vgcreate should be skipped.
	cmd.setResult("vgs sysvg", []byte("  sysvg"), nil)
	mgr := NewManager(cmd)
	layout := &config.PartitionLayout{
		Table:      "gpt",
		Partitions: []config.Partition{{Label: "pv", SizeMB: 8192}},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			PVPartition: 1,
			Volumes:     []config.LVVolume{{Name: "root", SizeMB: 4096, Filesystem: "ext4", Mountpoint: "/"}},
		},
	}

	err := mgr.ApplyLVMConfig(t.Context(), "/dev/sda", layout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, c := range cmd.calls {
		if c.name == "vgcreate" {
			t.Error("vgcreate should not be called when VG already exists")
		}
	}
}
