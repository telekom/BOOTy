//go:build linux

package blockdev

import "testing"

func TestPartitionDevicePath(t *testing.T) {
	tests := []struct {
		name string
		dev  string
		num  int
		want string
	}{
		{name: "sata", dev: "/dev/sda", num: 1, want: "/dev/sda1"},
		{name: "nvme", dev: "/dev/nvme0n1", num: 2, want: "/dev/nvme0n1p2"},
		{name: "loop", dev: "/dev/loop0", num: 1, want: "/dev/loop0p1"},
		{name: "mmc", dev: "/dev/mmcblk0", num: 3, want: "/dev/mmcblk0p3"},
		{name: "md", dev: "/dev/md0", num: 1, want: "/dev/md0p1"},
		{name: "nbd", dev: "/dev/nbd0", num: 2, want: "/dev/nbd0p2"},
		{name: "by-id", dev: "/dev/disk/by-id/nvme-eui.0011223344556677", num: 1, want: "/dev/disk/by-id/nvme-eui.0011223344556677-part1"},
		{name: "by-path", dev: "/dev/disk/by-path/pci-0000:00:1f.2-ata-1", num: 2, want: "/dev/disk/by-path/pci-0000:00:1f.2-ata-1-part2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PartitionDevicePath(tt.dev, tt.num); got != tt.want {
				t.Fatalf("PartitionDevicePath(%q, %d) = %q, want %q", tt.dev, tt.num, got, tt.want)
			}
		})
	}
}
