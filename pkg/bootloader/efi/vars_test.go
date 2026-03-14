package efi

import (
	"encoding/binary"
	"testing"
)

func TestBootEntry_VarName(t *testing.T) {
	tests := []struct {
		num  int
		want string
	}{
		{0, "Boot0000-8be4df61-93ca-11d2-aa0d-00e098032b8c"},
		{1, "Boot0001-8be4df61-93ca-11d2-aa0d-00e098032b8c"},
		{0xFF, "Boot00FF-8be4df61-93ca-11d2-aa0d-00e098032b8c"},
	}
	for _, tt := range tests {
		e := &BootEntry{Num: tt.num, Label: "test", LoaderPath: `\EFI\test`}
		if got := e.VarName(); got != tt.want {
			t.Errorf("VarName(%d) = %q, want %q", tt.num, got, tt.want)
		}
	}
}

func TestBootEntry_Validate(t *testing.T) {
	tests := []struct {
		name    string
		entry   BootEntry
		wantErr bool
	}{
		{
			name:  "valid",
			entry: BootEntry{Num: 0, Label: "Ubuntu", LoaderPath: `\EFI\ubuntu\shimx64.efi`, Active: true},
		},
		{
			name:    "negative num",
			entry:   BootEntry{Num: -1, Label: "test", LoaderPath: `\EFI\test`},
			wantErr: true,
		},
		{
			name:    "empty label",
			entry:   BootEntry{Num: 0, Label: "", LoaderPath: `\EFI\test`},
			wantErr: true,
		},
		{
			name:    "empty loader",
			entry:   BootEntry{Num: 0, Label: "test", LoaderPath: ""},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildLoadOption(t *testing.T) {
	entry := &BootEntry{
		Num:        0,
		Label:      "GRUB",
		LoaderPath: `\EFI\grub\grubx64.efi`,
		Active:     true,
	}

	data, err := BuildLoadOption(entry)
	if err != nil {
		t.Fatalf("BuildLoadOption() error: %v", err)
	}

	attrs := binary.LittleEndian.Uint32(data[0:4])
	if attrs != attrNVBSRT {
		t.Errorf("EFI attrs = %#x, want %#x", attrs, attrNVBSRT)
	}

	loadAttrs := binary.LittleEndian.Uint32(data[4:8])
	if loadAttrs != 0x01 {
		t.Errorf("load attrs = %#x, want 0x01", loadAttrs)
	}

	if len(data) < 20 {
		t.Errorf("load option too short: %d bytes", len(data))
	}
}

func TestBuildLoadOption_inactive(t *testing.T) {
	entry := &BootEntry{
		Num:        1,
		Label:      "Test",
		LoaderPath: `\EFI\test`,
		Active:     false,
	}

	data, err := BuildLoadOption(entry)
	if err != nil {
		t.Fatalf("BuildLoadOption() error: %v", err)
	}

	loadAttrs := binary.LittleEndian.Uint32(data[4:8])
	if loadAttrs != 0 {
		t.Errorf("load attrs = %#x, want 0x00", loadAttrs)
	}
}

func TestBuildLoadOption_invalid(t *testing.T) {
	entry := &BootEntry{Num: 0, Label: "", LoaderPath: ""}
	_, err := BuildLoadOption(entry)
	if err == nil {
		t.Fatal("expected error for invalid entry")
	}
}

func TestEncodeUCS2(t *testing.T) {
	result := encodeUCS2("AB")
	if len(result) != 6 {
		t.Fatalf("encodeUCS2 length = %d, want 6", len(result))
	}
	if result[0] != 0x41 || result[1] != 0x00 {
		t.Errorf("first char: %02x %02x, want 41 00", result[0], result[1])
	}
	if result[2] != 0x42 || result[3] != 0x00 {
		t.Errorf("second char: %02x %02x, want 42 00", result[2], result[3])
	}
	if result[4] != 0x00 || result[5] != 0x00 {
		t.Errorf("null terminator: %02x %02x, want 00 00", result[4], result[5])
	}
}
