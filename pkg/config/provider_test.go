package config

import "testing"

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusInit, "init"},
		{StatusSuccess, "success"},
		{StatusError, "error"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("Status = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestMachineConfigDefaults(t *testing.T) {
	cfg := &MachineConfig{}

	if cfg.MinDiskSizeGB != 0 {
		t.Errorf("expected 0 min disk size, got %d", cfg.MinDiskSizeGB)
	}
	if cfg.Hostname != "" {
		t.Errorf("expected empty hostname, got %s", cfg.Hostname)
	}
	if cfg.ImageURLs != nil {
		t.Errorf("expected nil image URLs, got %v", cfg.ImageURLs)
	}
}

func TestParsePartitionLayoutRootInLVM(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
	layout, err := ParsePartitionLayout(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layout.LVM == nil {
		t.Fatal("expected lvm config")
	}
}

func TestParsePartitionLayoutMissingRootEverywhere(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"data","filesystem":"xfs","mountpoint":"/data"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error when no root mountpoint exists")
	}
}

func TestParsePartitionLayoutUnsupportedPartitionFilesystem(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesystem":"ntfs","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for unsupported partition filesystem")
	}
}

func TestParsePartitionLayoutUnsupportedLVMFilesystem(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","filesystem":"btrfs","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for unsupported lvm filesystem")
	}
}

func TestParsePartitionLayoutLvmPVPartitionExceedsCount(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":2,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for pvPartition exceeding partition count")
	}
}

func TestParsePartitionLayoutLvmPVPartitionMustNotDefineFilesystem(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192,"filesystem":"ext4"}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error when pv partition defines a filesystem")
	}
}

func TestParsePartitionLayoutLvmNegativeLVSize(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","sizeMB":-1,"filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for negative lvm volume size")
	}
}

func TestParsePartitionLayoutLvmSizeAndExtentsExclusive(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","sizeMB":1024,"extents":"100%FREE","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error when lvm volume sets both sizeMB and extents")
	}
}

func TestParsePartitionLayoutMountpointWhitespace(t *testing.T) {
	input := "{\"table\":\"gpt\",\"partitions\":[{\"label\":\"root\",\"filesystem\":\"ext4\",\"mountpoint\":\"/bad path\"}]}"
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for mountpoint with whitespace")
	}
}

func TestParsePartitionLayoutDuplicatePartitionMountpoints(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root1","filesystem":"ext4","mountpoint":"/"},{"label":"root2","filesystem":"xfs","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for duplicate partition mountpoints")
	}
}

func TestParsePartitionLayoutDuplicateMountpointAcrossPartitionAndLVM(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"},{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":2,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for duplicate mountpoint across partition and lvm volume")
	}
}

func TestParsePartitionLayoutSpecialCharLabel(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root#1","filesystem":"ext4","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for label with special characters")
	}
}

func TestParsePartitionLayoutDuplicateLabels(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":4096,"filesystem":"ext4","mountpoint":"/"},{"label":"root","sizeMB":4096,"filesystem":"xfs","mountpoint":"/data"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for duplicate partition labels")
	}
}

func TestParsePartitionLayoutLabelTooLong(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"this-label-is-way-too-long-for-a-gpt-partition-label-maximum","filesystem":"ext4","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Error("expected error for label exceeding 36 characters")
	}
}

func TestParsePartitionLayoutLvmVGNameMustNotStartWithDashOrDot(t *testing.T) {
	tests := []string{"-sysvg", ".sysvg", ".."}

	for _, vgName := range tests {
		input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192},{"label":"root","filesystem":"ext4","mountpoint":"/"}],"lvm":{"volumeGroup":"` + vgName + `","pvPartition":1,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"}]}}`
		_, err := ParsePartitionLayout(input)
		if err == nil {
			t.Fatalf("expected error for invalid volumeGroup %q", vgName)
		}
	}
}

func TestParsePartitionLayoutLvmLVNameMustNotStartWithDashOrDot(t *testing.T) {
	tests := []string{"-root", ".root", ".."}

	for _, lvName := range tests {
		input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"` + lvName + `","filesystem":"ext4","mountpoint":"/"}]}}`
		_, err := ParsePartitionLayout(input)
		if err == nil {
			t.Fatalf("expected error for invalid lvm volume name %q", lvName)
		}
	}
}

func TestParsePartitionLayoutRejectsUnknownFields(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesytem":"ext4","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for unknown field in partition layout")
	}
}

func TestParsePartitionLayoutDuplicateLVMVolumeNames(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"},{"name":"root","filesystem":"xfs","mountpoint":"/var"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for duplicate lvm volume names")
	}
}

func TestParsePartitionLayoutFillRemainingLVMVolumeMustBeLast(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","filesystem":"ext4","mountpoint":"/"},{"name":"var","sizeMB":1024,"filesystem":"xfs","mountpoint":"/var"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error when fill-remaining lvm volume is not last")
	}
}

func TestParsePartitionLayoutTrailingContent(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}{"extra":true}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for trailing JSON content")
	}
}

func TestParsePartitionLayoutInvalidExtentsFormat(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","extents":"foo bar","filesystem":"ext4","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for invalid extents format")
	}
}
