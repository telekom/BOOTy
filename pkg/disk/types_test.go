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
