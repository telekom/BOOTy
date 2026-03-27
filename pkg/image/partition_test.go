//go:build linux

package image

import "testing"

func TestTargetPartitionNode(t *testing.T) {
	tests := []struct {
		name    string
		disk    string
		partNum int
		want    string
	}{
		{"sda partition 1", "/dev/sda", 1, "/dev/sda1"},
		{"sda partition 2", "/dev/sda", 2, "/dev/sda2"},
		{"nvme partition 1", "/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"nvme partition 3", "/dev/nvme0n1", 3, "/dev/nvme0n1p3"},
		{"loop partition 1", "/dev/loop0", 1, "/dev/loop0p1"},
		{"vda partition 2", "/dev/vda", 2, "/dev/vda2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := targetPartitionNode(tt.disk, tt.partNum)
			if got != tt.want {
				t.Errorf("targetPartitionNode(%q, %d) = %q, want %q", tt.disk, tt.partNum, got, tt.want)
			}
		})
	}
}

func TestConvertQCOW2HookRegistered(t *testing.T) {
	// On linux, the init() in qcow2.go should have set the hook.
	if convertQCOW2Hook == nil {
		t.Fatal("convertQCOW2Hook is nil on linux")
	}
}
