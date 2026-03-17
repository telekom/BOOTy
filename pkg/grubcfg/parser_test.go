package grubcfg

import "testing"

func TestParse(t *testing.T) {
	text := `
menuentry 'Ubuntu' {
    linux /vmlinuz root=/dev/sda1 ro quiet
    initrd /initrd.img
}
menuentry 'Rescue' {
    linux /vmlinuz root=/dev/sda1 ro single
    initrd /initrd.img
}
`
	entries, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Title != "Ubuntu" {
		t.Errorf("title = %q", entries[0].Title)
	}
	if entries[0].Linux != "/vmlinuz" {
		t.Errorf("linux = %q", entries[0].Linux)
	}
	if entries[0].Args != "root=/dev/sda1 ro quiet" {
		t.Errorf("args = %q", entries[0].Args)
	}
	if entries[0].Initrd != "/initrd.img" {
		t.Errorf("initrd = %q", entries[0].Initrd)
	}
	if entries[1].Title != "Rescue" {
		t.Errorf("title[1] = %q", entries[1].Title)
	}
}

func TestParseEmpty(t *testing.T) {
	e, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if len(e) != 0 {
		t.Errorf("got %d entries, want 0", len(e))
	}
}

func TestExtractMenuEntryTitle(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{`menuentry 'Ubuntu 22.04' {`, "Ubuntu 22.04"},
		{`menuentry "CentOS" --class centos {`, "CentOS"},
		{`  menuentry 'Indented' {`, "Indented"},
		{`not a menuentry`, ""},
	}
	for _, tc := range tests {
		got := ExtractMenuEntryTitle(tc.line)
		if got != tc.want {
			t.Errorf("ExtractMenuEntryTitle(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestParseCommentWithBrace(t *testing.T) {
	text := `
menuentry 'Ubuntu' {
    # closing brace in comment: }
    linux /vmlinuz root=/dev/sda1
    initrd /initrd.img
}
`
	entries, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Linux != "/vmlinuz" {
		t.Errorf("linux = %q, want /vmlinuz", entries[0].Linux)
	}
}

func TestParseLinuxefi(t *testing.T) {
	text := `
menuentry 'EFI Boot' {
    linuxefi /vmlinuz root=/dev/sda1 ro
    initrd /initrd.img
}
`
	entries, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	// linuxefi starts with "linux" so the prefix match should still work.
	if entries[0].Linux != "/vmlinuz" {
		t.Errorf("linux = %q, want /vmlinuz", entries[0].Linux)
	}
}

func TestParseNoInitrd(t *testing.T) {
	text := `
menuentry 'Kernel Only' {
    linux /vmlinuz root=/dev/sda1
}
`
	entries, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Initrd != "" {
		t.Errorf("initrd = %q, want empty", entries[0].Initrd)
	}
}
