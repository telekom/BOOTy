//go:build linux

package provision

import (
	"context"
	"errors"
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

func TestResolveFstabSource(t *testing.T) {
	tests := []struct {
		source   string
		blkidKey string
		out      string
		err      error
		want     string
	}{
		{source: "UUID=abcd", blkidKey: "blkid -U", out: "/dev/vda16\n", want: "/dev/vda16"},
		{source: "PARTUUID=1234", blkidKey: "blkid -t", out: "/dev/vda16\n", want: "/dev/vda16"},
		{source: "LABEL=BOOT", blkidKey: "blkid -L", out: "/dev/vda16\n", want: "/dev/vda16"},
		{source: "/dev/sda2", want: "/dev/sda2"},
		{source: "LABEL=BOOT", blkidKey: "blkid -L", err: errors.New("not found")},
		{source: "LABEL=BOOT", blkidKey: "blkid -L", out: "garbage\n"},
		{source: "tmpfs"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
			if tt.blkidKey != "" {
				cmd.setResult(tt.blkidKey, []byte(tt.out), tt.err)
			}
			if got := o.resolveFstabSource(context.Background(), tt.source); got != tt.want {
				t.Fatalf("resolveFstabSource(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
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
