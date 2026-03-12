package kexec

import (
	"strings"
	"testing"
)

const sampleGrubCfg = `
### BEGIN /etc/grub.d/10_linux ###
menuentry 'Ubuntu' --class ubuntu --class gnu-linux --class gnu --class os {
	recordfail
	load_video
	gfxmode $linux_gfx_mode
	insmod gzio
	linux /boot/vmlinuz-5.15.0-generic root=/dev/mapper/vg0-root ro quiet
	initrd /boot/initrd.img-5.15.0-generic
}
menuentry 'Ubuntu, with Linux 5.15.0-generic (recovery mode)' --class ubuntu {
	recordfail
	linux /boot/vmlinuz-5.15.0-generic root=/dev/mapper/vg0-root ro recovery nomodeset
	initrd /boot/initrd.img-5.15.0-generic
}
### END /etc/grub.d/10_linux ###
`

func TestParseGrubCfg(t *testing.T) {
	entries, err := ParseGrubCfg(strings.NewReader(sampleGrubCfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.Name != "Ubuntu" {
		t.Errorf("name = %q, want %q", e.Name, "Ubuntu")
	}
	if e.Kernel != "/boot/vmlinuz-5.15.0-generic" {
		t.Errorf("kernel = %q", e.Kernel)
	}
	if e.Initramfs != "/boot/initrd.img-5.15.0-generic" {
		t.Errorf("initramfs = %q", e.Initramfs)
	}
	if !strings.Contains(e.KernelArgs, "root=/dev/mapper/vg0-root") {
		t.Errorf("kernel args = %q", e.KernelArgs)
	}
}

func TestParseGrubCfgLinuxEFI(t *testing.T) {
	cfg := `menuentry "Ubuntu EFI" {
	linuxefi /boot/vmlinuz root=UUID=abc ro
	initrdefi /boot/initrd.img
}
`
	entries, err := ParseGrubCfg(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Kernel != "/boot/vmlinuz" {
		t.Errorf("kernel = %q", entries[0].Kernel)
	}
	if entries[0].Initramfs != "/boot/initrd.img" {
		t.Errorf("initramfs = %q", entries[0].Initramfs)
	}
}

func TestParseGrubCfgLinux16(t *testing.T) {
	cfg := `menuentry "Legacy" {
	linux16 /boot/vmlinuz console=tty0
	initrd16 /boot/initrd.img
}
`
	entries, err := ParseGrubCfg(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Kernel != "/boot/vmlinuz" {
		t.Errorf("kernel = %q", entries[0].Kernel)
	}
	if entries[0].KernelArgs != "console=tty0" {
		t.Errorf("args = %q", entries[0].KernelArgs)
	}
}

func TestParseGrubCfgEmpty(t *testing.T) {
	entries, err := ParseGrubCfg(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetDefaultEntry(t *testing.T) {
	entries := []BootEntry{
		{Name: "first", Kernel: "/boot/vmlinuz-1"},
		{Name: "second", Kernel: "/boot/vmlinuz-2"},
	}
	got, err := GetDefaultEntry(entries)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "first" {
		t.Errorf("got %q, want %q", got.Name, "first")
	}
}

func TestGetDefaultEntryEmpty(t *testing.T) {
	_, err := GetDefaultEntry(nil)
	if err == nil {
		t.Fatal("expected error for empty entries")
	}
}

func TestExtractMenuEntryName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"menuentry 'Ubuntu' --class os {", "Ubuntu"},
		{`menuentry "Debian GNU/Linux" --class debian {`, "Debian GNU/Linux"},
		{"menuentry Fallback {", "Fallback"},
	}
	for _, tt := range tests {
		got := extractMenuEntryName(tt.line)
		if got != tt.want {
			t.Errorf("extractMenuEntryName(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestParseGrubCfgNoClosingBrace(t *testing.T) {
	cfg := `menuentry 'Orphan' {
	linux /boot/vmlinuz root=/dev/sda1
	initrd /boot/initrd.img
`
	entries, err := ParseGrubCfg(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for unclosed brace, got %d", len(entries))
	}
	if entries[0].Name != "Orphan" {
		t.Errorf("name = %q", entries[0].Name)
	}
}
