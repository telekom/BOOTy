package disk

import "testing"

func TestPartitionNumber(t *testing.T) {
	tests := []struct {
		node string
		disk string
		want int
	}{
		{"/dev/sda1", "/dev/sda", 1},
		{"/dev/sda2", "/dev/sda", 2},
		{"/dev/sda10", "/dev/sda", 10},
		{"/dev/nvme0n1p1", "/dev/nvme0n1", 1},
		{"/dev/nvme0n1p2", "/dev/nvme0n1", 2},
		{"/dev/nvme0n1p15", "/dev/nvme0n1", 15},
		{"/dev/vda3", "/dev/vda", 3},
		{"/dev/sda2", "/dev/disk/by-id/osdisk", 2},
		{"/dev/nvme0n1p3", "/dev/disk/by-id/nvme-osdisk", 3},
		{"/dev/disk/by-id/osdisk-part2", "/dev/disk/by-id/osdisk", 2},
		{"/dev/disk/by-path/pci-0000:00:1f.2-ata-1-part10", "/dev/disk/by-path/pci-0000:00:1f.2-ata-1", 10},
	}
	for _, tt := range tests {
		t.Run(tt.node, func(t *testing.T) {
			got := PartitionNumber(tt.node, tt.disk)
			if got != tt.want {
				t.Errorf("PartitionNumber(%q, %q) = %d, want %d", tt.node, tt.disk, got, tt.want)
			}
		})
	}
}

func TestPartitionNumberCheckedErrors(t *testing.T) {
	tests := []struct {
		name string
		node string
		disk string
	}{
		{name: "empty node", disk: "/dev/sda"},
		{name: "empty disk", node: "/dev/sda1"},
		{name: "whole disk", node: "/dev/sda", disk: "/dev/sda"},
		{name: "malformed prefix suffix", node: "/dev/sdaa2", disk: "/dev/sda"},
		{name: "no trailing number", node: "/dev/mapper/root", disk: "/dev/disk/by-id/osdisk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PartitionNumberChecked(tt.node, tt.disk); err == nil {
				t.Fatalf("PartitionNumberChecked(%q, %q) error = nil, want error", tt.node, tt.disk)
			}
		})
	}
}
