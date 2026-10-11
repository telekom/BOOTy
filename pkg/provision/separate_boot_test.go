//go:build linux

package provision

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

// Legacy evidence: provision.sh:366-395 mounts /boot from the image fstab
// when the root filesystem has no /boot/grub.
func TestSeparateBootSource(t *testing.T) {
	tests := []struct {
		name       string
		grubOnRoot bool
		fstab      string
		wantNeeded bool
		wantSource string
	}{
		{name: "grub on root", grubOnRoot: true, fstab: "LABEL=BOOT /boot ext4 defaults 0 2\n"},
		{
			name:       "separate boot by label",
			fstab:      "# comment\nLABEL=cloudimg-rootfs / ext4 discard 0 1\nLABEL=BOOT /boot ext4 defaults 0 2\nLABEL=UEFI /boot/efi vfat umask=0077 0 1\n",
			wantNeeded: true,
			wantSource: "LABEL=BOOT",
		},
		{name: "no boot entry", fstab: "UUID=1 / ext4 defaults 0 1\n#LABEL=BOOT /boot ext4 defaults 0 2\n", wantNeeded: true},
		{name: "no fstab", wantNeeded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "boot"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.grubOnRoot {
				if err := os.MkdirAll(filepath.Join(root, "boot", "grub"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.fstab != "" {
				writeTestFile(t, filepath.Join(root, "etc", "fstab"), tt.fstab)
			}
			needed, source, err := separateBootSource(root)
			if err != nil {
				t.Fatalf("separateBootSource() error = %v", err)
			}
			if needed != tt.wantNeeded || source != tt.wantSource {
				t.Fatalf("separateBootSource() = (%v, %q), want (%v, %q)", needed, source, tt.wantNeeded, tt.wantSource)
			}
		})
	}
}

func TestSeparateBootSourceIgnoresSymlinks(t *testing.T) {
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "fstab"), "LABEL=BOOT /boot ext4 defaults 0 2\n")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "etc")); err != nil {
		t.Fatal(err)
	}
	if _, source, err := separateBootSource(root); err != nil || source != "" {
		t.Fatalf("separateBootSource() = %q, %v; must not read an fstab outside the root", source, err)
	}

	root2 := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root2, "boot")); err != nil {
		t.Fatal(err)
	}
	if needed, _, err := separateBootSource(root2); err != nil || needed {
		t.Fatalf("separateBootSource() = %v, %v; a /boot symlink must not be mounted over", needed, err)
	}
}

func TestParseBlkidExport(t *testing.T) {
	got := parseBlkidExport("DEVNAME=/dev/vda2\nLABEL=BOOT\nUUID=abcd\nTYPE=ext4\nPARTUUID=1234\n")
	if got.label != "BOOT" || got.uuid != "abcd" || got.partUUID != "1234" {
		t.Fatalf("parseBlkidExport() = %+v", got)
	}
}

// Only one partition of the target disk may match, so a generic label on
// another disk or a cloned UUID is never mounted.
func TestMatchFstabSource(t *testing.T) {
	devs := []blockIDs{
		{node: "/dev/vda1", label: "UEFI", uuid: "1111"},
		{node: "/dev/vda2", label: "BOOT", uuid: "2222", partUUID: "p2"},
		{node: "/dev/vda3", label: "root", uuid: "3333"},
		{node: "/dev/vda4", label: "dup", uuid: "4444"},
		{node: "/dev/vda5", label: "dup", uuid: "5555"},
	}
	tests := []struct{ source, want string }{
		{"LABEL=BOOT", "/dev/vda2"},
		{"UUID=2222", "/dev/vda2"},
		{"PARTUUID=p2", "/dev/vda2"},
		{"/dev/vda2", "/dev/vda2"},
		{"/dev/sdb1", ""},
		{"LABEL=dup", ""},
		{"LABEL=missing", ""},
		{"tmpfs", ""},
	}
	for _, tt := range tests {
		if got := matchFstabSource(tt.source, devs); got != tt.want {
			t.Errorf("matchFstabSource(%q) = %q, want %q", tt.source, got, tt.want)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveFstabSourceDeviceSymlink(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "vda2")
	writeTestFile(t, node, "")
	link := filepath.Join(dir, "by-uuid-abcd")
	if err := os.Symlink(node, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := matchFstabSource(resolved, []blockIDs{{node: node}}); got != node {
		t.Fatalf("matchFstabSource(%q) = %q, want %q", resolved, got, node)
	}
}

// A declared partition layout owns /boot and the generated fstab.
func TestMountSeparateBootSkipsPartitionLayout(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	if err := o.mountSeparateBoot(context.Background()); err != nil {
		t.Fatalf("mountSeparateBoot() = %v", err)
	}
	if len(cmd.calls) != 0 || o.separateBootMounted {
		t.Fatalf("partition layout run touched /boot: %v", cmd.calls)
	}
}
