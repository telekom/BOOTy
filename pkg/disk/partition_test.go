//go:build linux

package disk

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestParsePartitionLayout(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		parts   int
	}{
		{
			name:    "valid layout",
			input:   `{"table":"gpt","partitions":[{"label":"efi","sizeMB":512,"filesystem":"vfat","mountpoint":"/boot/efi"},{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`,
			wantErr: false,
			parts:   2,
		},
		{
			name:    "default table type",
			input:   `{"partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`,
			wantErr: false,
			parts:   1,
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
	if !contains(fstab, "/dev/sda1") || !contains(fstab, "/boot/efi") {
		t.Errorf("fstab missing EFI entry:\n%s", fstab)
	}
	if !contains(fstab, "/dev/sda2") || !contains(fstab, "ext4") {
		t.Errorf("fstab missing root entry:\n%s", fstab)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
