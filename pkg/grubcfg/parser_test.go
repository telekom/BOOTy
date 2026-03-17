package grubcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cfg := `
menuentry 'Ubuntu 22.04' --class ubuntu {
	linux /vmlinuz root=UUID=abc ro quiet
	initrd /initrd.img
}
menuentry Legacy {
	linux16 /vmlinuz-legacy root=/dev/sda1 ro
	initrd16 /initrd-legacy.img
} # end
`

	entries, err := Parse(strings.NewReader(cfg))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Title != "Ubuntu 22.04" {
		t.Fatalf("title[0] = %q, want Ubuntu 22.04", entries[0].Title)
	}
	if entries[0].Kernel != "/vmlinuz" {
		t.Fatalf("kernel[0] = %q, want /vmlinuz", entries[0].Kernel)
	}
	if entries[1].Title != "Legacy" {
		t.Fatalf("title[1] = %q, want Legacy", entries[1].Title)
	}
	if entries[1].Kernel != "/vmlinuz-legacy" {
		t.Fatalf("kernel[1] = %q, want /vmlinuz-legacy", entries[1].Kernel)
	}
}

func TestParseNoClosingBrace(t *testing.T) {
	cfg := `menuentry 'Orphan' {
	linux /vmlinuz root=/dev/sda1 ro
	initrd /initrd.img
`

	entries, err := Parse(strings.NewReader(cfg))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Title != "Orphan" {
		t.Fatalf("title = %q, want Orphan", entries[0].Title)
	}
}

func TestParseFileMissing(t *testing.T) {
	if _, err := ParseFile("/does/not/exist/grub.cfg"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grub.cfg")
	content := "menuentry 'One' {\nlinux /vmlinuz root=UUID=abc ro\ninitrd /initrd.img\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write grub.cfg: %v", err)
	}

	entries, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
}

func TestExtractMenuEntryTitle(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{line: "menuentry 'Ubuntu' --class os {", want: "Ubuntu"},
		{line: `menuentry "Debian GNU/Linux" --class debian {`, want: "Debian GNU/Linux"},
		{line: "menuentry Fallback {", want: "Fallback"},
	}

	for _, tt := range tests {
		if got := ExtractMenuEntryTitle(tt.line); got != tt.want {
			t.Fatalf("ExtractMenuEntryTitle(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}
