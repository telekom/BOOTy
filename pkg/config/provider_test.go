package config

import (
	"fmt"
	"strings"
	"testing"
)

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

	if cfg.Provision.Disk.MinSizeGB != 0 {
		t.Errorf("expected 0 min disk size, got %d", cfg.Provision.Disk.MinSizeGB)
	}
	if cfg.Hostname != "" {
		t.Errorf("expected empty hostname, got %s", cfg.Hostname)
	}
	if cfg.Provision.Image.URLs != nil {
		t.Errorf("expected nil image URLs, got %v", cfg.Provision.Image.URLs)
	}
	if DefaultCrashArtifactsMaxMB != 256 {
		t.Errorf("DefaultCrashArtifactsMaxMB = %d, want 256", DefaultCrashArtifactsMaxMB)
	}
	if DefaultCrashArtifactsUploadTimeoutSec != 120 {
		t.Errorf("DefaultCrashArtifactsUploadTimeoutSec = %d, want 120", DefaultCrashArtifactsUploadTimeoutSec)
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

func TestParsePartitionLayoutTrimmedDevice(t *testing.T) {
	input := `{"table":"gpt","device":"  /dev/sda  ","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`
	layout, err := ParsePartitionLayout(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layout.Device != "/dev/sda" {
		t.Fatalf("device = %q, want /dev/sda", layout.Device)
	}
}

func TestParsePartitionLayoutRejectsRelativeDevice(t *testing.T) {
	input := `{"table":"gpt","device":"dev/sda","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for relative device path")
	}
}

func TestParsePartitionLayoutRejectsDeviceWithWhitespace(t *testing.T) {
	input := `{"table":"gpt","device":"/dev/my disk","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for whitespace in device path")
	}
}

func TestParsePartitionLayoutMountpointRequiresFilesystem(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","mountpoint":"/"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error when mountpoint is set without filesystem")
	}
}

func TestParsePartitionLayoutLVMMountpointRequiresFilesystem(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"pv","sizeMB":8192}],"lvm":{"volumeGroup":"sysvg","pvPartition":1,"volumes":[{"name":"root","mountpoint":"/"}]}}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error when lvm mountpoint is set without filesystem")
	}
}

func TestParsePartitionLayoutInvalidTypeGUID(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":0,"mountpoint":"/","filesystem":"ext4","typeGUID":"not-a-guid"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for invalid typeGUID")
	}
}

func TestParsePartitionLayoutValidTypeGUID(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":0,"mountpoint":"/","filesystem":"ext4","typeGUID":"0FC63DAF-8483-4772-8E79-3D69D8477DE4"}]}`
	layout, err := ParsePartitionLayout(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layout.Partitions[0].TypeGUID != "0FC63DAF-8483-4772-8E79-3D69D8477DE4" {
		t.Errorf("TypeGUID = %q, want valid UUID", layout.Partitions[0].TypeGUID)
	}
}

func TestParsePartitionLayoutRejectsDeviceTraversal(t *testing.T) {
	input := `{"table":"gpt","device":"/dev/../etc/passwd","partitions":[{"label":"root","sizeMB":0,"mountpoint":"/","filesystem":"ext4"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for device path with ..")
	}
}

func TestParsePartitionLayoutRejectsMountpointTraversal(t *testing.T) {
	input := `{"table":"gpt","partitions":[{"label":"root","sizeMB":0,"mountpoint":"/boot/../../etc","filesystem":"ext4"}]}`
	_, err := ParsePartitionLayout(input)
	if err == nil {
		t.Fatal("expected error for mountpoint with ..")
	}
}

func TestParsePartitionLayoutTooManyPartitions(t *testing.T) {
	parts := `{"table":"gpt","partitions":[`
	for i := range 129 {
		if i > 0 {
			parts += ","
		}
		parts += fmt.Sprintf(`{"label":"p%d","sizeMB":100,"filesystem":"ext4","mountpoint":"/mnt/p%d"}`, i, i)
	}
	parts += `]}`

	_, err := ParsePartitionLayout(parts)
	if err == nil {
		t.Fatal("expected error for too many partitions")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "empty config is valid", cfg: Config{}},
		{name: "valid mode provision", cfg: Config{Mode: "provision"}},
		{name: "valid mode dry-run", cfg: Config{Mode: "dry-run"}},
		{name: "invalid mode", cfg: Config{Mode: "invalid"}, wantErr: "invalid mode"},
		{name: "valid image mode", cfg: Config{Provision: ProvisionConfig{Image: ImageConfig{Mode: "whole-disk"}}}},
		{name: "invalid image mode", cfg: Config{Provision: ProvisionConfig{Image: ImageConfig{Mode: "raw"}}}, wantErr: "invalid provision.image.mode"},
		{name: "valid network mode", cfg: Config{Network: NetworkConfig{Mode: "gobgp"}}},
		{name: "invalid network mode", cfg: Config{Network: NetworkConfig{Mode: "ospf"}}, wantErr: "invalid network.mode"},
		{name: "valid checksum type", cfg: Config{Provision: ProvisionConfig{Image: ImageConfig{ChecksumType: "sha256"}}}},
		{name: "invalid checksum type", cfg: Config{Provision: ProvisionConfig{Image: ImageConfig{ChecksumType: "md5"}}}, wantErr: "invalid provision.image.checksumType"},
		{name: "valid rescue mode", cfg: Config{Rescue: RescueConfig{Mode: "shell"}}},
		{name: "invalid rescue mode", cfg: Config{Rescue: RescueConfig{Mode: "panic"}}, wantErr: "invalid rescue.mode"},
		{name: "valid peer mode", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{PeerMode: "unnumbered"}}}},
		{name: "valid dual peer mode with neighbors", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{PeerMode: "dual", Neighbors: "10.0.0.1"}}}},
		{name: "valid numbered peer mode with neighbors", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{PeerMode: "numbered", Neighbors: "10.0.0.1"}}}},
		{name: "dual peer mode requires neighbors", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{PeerMode: "dual"}}}, wantErr: "network.bgp.neighbors required"},
		{name: "numbered peer mode requires neighbors", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{PeerMode: "numbered"}}}, wantErr: "network.bgp.neighbors required"},
		{name: "invalid peer mode", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{PeerMode: "mesh"}}}, wantErr: "invalid network.bgp.peerMode"},
		{name: "valid underlay AF", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{UnderlayAF: "ipv4"}}}},
		{name: "invalid underlay AF", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{UnderlayAF: "ipv3"}}}, wantErr: "invalid network.bgp.underlayAF"},
		{name: "valid overlay type", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{OverlayType: "evpn-vxlan"}}}},
		{name: "invalid overlay type", cfg: Config{Network: NetworkConfig{BGP: BGPConfig{OverlayType: "gre"}}}, wantErr: "invalid network.bgp.overlayType"},
		{name: "valid cloud-init ds", cfg: Config{Provision: ProvisionConfig{CloudInit: CloudInitConfig{Datasource: "nocloud"}}}},
		{name: "invalid cloud-init ds", cfg: Config{Provision: ProvisionConfig{CloudInit: CloudInitConfig{Datasource: "ec2"}}}, wantErr: "invalid provision.cloudInit.datasource"},
		{name: "valid sysext preload mode", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{DefaultMode: "preload"}}}},
		{name: "invalid sysext default mode", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{DefaultMode: "enabled"}}}, wantErr: "invalid provision.sysext.defaultMode"},
		{name: "invalid sysext catalog dir", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{CatalogDir: "var/lib/sysext"}}}, wantErr: "provision.sysext.catalogDir"},
		{name: "invalid sysext active dir", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{ActiveDir: "/"}}}, wantErr: "provision.sysext.activeDir"},
		{name: "invalid sysext layer mode", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{Layers: []SysextLayerConfig{{Name: "debug", Mode: "now"}}}}}, wantErr: "invalid provision.sysext.layers[0].mode"},
		{name: "invalid sysext filename", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{Layers: []SysextLayerConfig{{Name: "debug", FileName: "../debug.raw"}}}}}, wantErr: "provision.sysext.layers[0].fileName"},
		{name: "enabled sysext layer requires source", cfg: Config{Provision: ProvisionConfig{Sysext: SysextConfig{Enabled: true, Layers: []SysextLayerConfig{{Name: "debug"}}}}}, wantErr: "source is required"},
		{name: "valid token algorithm", cfg: Config{Transport: TransportConfig{TokenAlgorithm: "ES256"}}},
		{name: "invalid token algorithm", cfg: Config{Transport: TransportConfig{TokenAlgorithm: "HS256"}}, wantErr: "invalid transport.tokenAlgorithm"},
		{
			name:    "multiple errors",
			cfg:     Config{Mode: "bad", Provision: ProvisionConfig{Image: ImageConfig{Mode: "bad"}}},
			wantErr: "invalid mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}
