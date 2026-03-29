//go:build linux

package bootloader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGRUBListEntries(t *testing.T) {
	root := t.TempDir()
	grubDir := filepath.Join(root, "boot", "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	grubCfg := `menuentry 'Ubuntu' {
	linux /vmlinuz root=/dev/sda1 ro quiet
	initrd /initrd.img
}
menuentry 'Ubuntu (recovery mode)' {
	linux /vmlinuz root=/dev/sda1 ro single
	initrd /initrd.img
}
`
	if err := os.WriteFile(filepath.Join(grubDir, "grub.cfg"), []byte(grubCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &GRUB{}
	entries, err := g.ListEntries(root)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Title != "Ubuntu" {
		t.Errorf("entry[0].Title = %q, want Ubuntu", entries[0].Title)
	}
	if entries[1].Title != "Ubuntu (recovery mode)" {
		t.Errorf("entry[1].Title = %q, want Ubuntu (recovery mode)", entries[1].Title)
	}
}

func TestGRUBListEntries_MissingFile(t *testing.T) {
	root := t.TempDir()
	g := &GRUB{}
	_, err := g.ListEntries(root)
	if err == nil {
		t.Error("expected error for missing grub.cfg")
	}
}

func TestSystemdBootConfigure_NoOp(t *testing.T) {
	s := &SystemdBoot{}
	err := s.Configure("/nonexistent", BootConfig{
		DefaultEntry: "test",
		Cmdline:      "root=/dev/sda1",
		KernelPath:   "/boot/vmlinuz",
		InitrdPath:   "/boot/initrd.img",
	})
	if err != nil {
		t.Errorf("Configure should be a no-op, got: %v", err)
	}
}

func TestParseBootctlOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int
	}{
		{
			name:   "empty output",
			output: "",
			want:   0,
		},
		{
			name:   "single entry",
			output: "title: Ubuntu 22.04\nlinux: /vmlinuz-5.15.0\ninitrd: /initrd.img-5.15.0\noptions: root=/dev/sda2 ro quiet",
			want:   1,
		},
		{
			name:   "multiple entries",
			output: "title: Ubuntu 22.04\nlinux: /vmlinuz-5.15.0\ninitrd: /initrd.img-5.15.0\noptions: root=/dev/sda2 ro quiet\n\ntitle: Ubuntu 22.04 (fallback)\nlinux: /vmlinuz-5.15.0\ninitrd: /initrd.img-5.15.0\noptions: root=/dev/sda2 ro single",
			want:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := parseBootctlOutput(tt.output)
			if len(entries) != tt.want {
				t.Errorf("got %d entries, want %d", len(entries), tt.want)
			}
		})
	}
}

func TestParseBootctlOutput_FieldValues(t *testing.T) {
	output := "title: Test Entry\nlinux: /vmlinuz-test\ninitrd: /initrd-test\noptions: root=/dev/sda2 ro"
	entries := parseBootctlOutput(output)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Title != "Test Entry" {
		t.Errorf("Title = %q, want Test Entry", e.Title)
	}
	if e.Kernel != "/vmlinuz-test" {
		t.Errorf("Kernel = %q, want /vmlinuz-test", e.Kernel)
	}
	if e.Initrd != "/initrd-test" {
		t.Errorf("Initrd = %q, want /initrd-test", e.Initrd)
	}
	if e.Args != "root=/dev/sda2 ro" {
		t.Errorf("Args = %q, want root=/dev/sda2 ro", e.Args)
	}
}

func TestDetectBootloader_DirectoryNotFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "boot", "efi", "EFI", "systemd", "systemd-bootx64.efi")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	b := DetectBootloader(root)
	if _, ok := b.(*GRUB); !ok {
		t.Errorf("expected GRUB when EFI path is directory, got %T", b)
	}
}
