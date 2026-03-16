//go:build linux

package bootloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGRUBConfig(t *testing.T) {
	grubCfg := `
menuentry 'Ubuntu 22.04' --class ubuntu {
	linux /vmlinuz-5.15.0-generic root=UUID=abc-123 ro quiet
	initrd /initrd.img-5.15.0-generic
}
menuentry 'Ubuntu 22.04 (recovery)' --class ubuntu {
	linux /vmlinuz-5.15.0-generic root=UUID=abc-123 ro single
	initrd /initrd.img-5.15.0-generic
}
`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "grub.cfg")
	if err := os.WriteFile(cfgPath, []byte(grubCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := parseGRUBConfig(cfgPath)
	if err != nil {
		t.Fatalf("parseGRUBConfig: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	if entries[0].Title != "Ubuntu 22.04" {
		t.Errorf("title = %q, want %q", entries[0].Title, "Ubuntu 22.04")
	}
	if entries[0].Kernel != "/vmlinuz-5.15.0-generic" {
		t.Errorf("kernel = %q", entries[0].Kernel)
	}
	if entries[0].Initrd != "/initrd.img-5.15.0-generic" {
		t.Errorf("initrd = %q", entries[0].Initrd)
	}
	if entries[0].Cmdline != "root=UUID=abc-123 ro quiet" {
		t.Errorf("cmdline = %q", entries[0].Cmdline)
	}
}

func TestParseGRUBConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "grub.cfg")
	if err := os.WriteFile(cfgPath, []byte("# empty config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := parseGRUBConfig(cfgPath)
	if err != nil {
		t.Fatalf("parseGRUBConfig: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %d, want 0", len(entries))
	}
}

func TestParseGRUBConfig_Missing(t *testing.T) {
	_, err := parseGRUBConfig("/nonexistent/grub.cfg")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestExtractQuoted(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"menuentry 'Ubuntu 22.04' --class ubuntu {", "Ubuntu 22.04"},
		{"menuentry 'Recovery Mode' {", "Recovery Mode"},
		{"no quotes here", ""},
		{"single 'quote", ""},
		{`menuentry "CentOS 7" --class centos {`, "CentOS 7"},
	}

	for _, tc := range tests {
		got := extractQuoted(tc.input)
		if got != tc.expected {
			t.Errorf("extractQuoted(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseGRUBConfig_LongKernelLine(t *testing.T) {
	longArg := strings.Repeat("a", 70*1024)
	grubCfg := "menuentry 'Long Kernel' {\n" +
		"\tlinux /vmlinuz root=UUID=abc ro extra=" + longArg + "\n" +
		"\tinitrd /initrd.img\n" +
		"}\n"

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "grub.cfg")
	if err := os.WriteFile(cfgPath, []byte(grubCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := parseGRUBConfig(cfgPath)
	if err != nil {
		t.Fatalf("parseGRUBConfig: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Title != "Long Kernel" {
		t.Fatalf("title = %q, want Long Kernel", entries[0].Title)
	}
}
