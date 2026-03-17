package efi

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeEFIVar(t *testing.T, dir, name string, attrs uint32, data []byte) {
	t.Helper()
	buf := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(buf[:4], attrs)
	copy(buf[4:], data)
	if err := os.WriteFile(filepath.Join(dir, name), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadVar(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "TestVar-1234", 7, []byte{0x42, 0x43})
	r := NewEFIVarReader(dir)
	val, err := r.ReadVar("TestVar-1234")
	if err != nil {
		t.Fatal(err)
	}
	if len(val) != 2 || val[0] != 0x42 {
		t.Errorf("val = %v", val)
	}
}

func TestWriteVar(t *testing.T) {
	dir := t.TempDir()
	r := NewEFIVarReader(dir)
	if err := r.WriteVar("NewVar-5678", 3, []byte{0xAA}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "NewVar-5678"))
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(data[:4]) != 3 {
		t.Errorf("attrs = %d", binary.LittleEndian.Uint32(data[:4]))
	}
	if data[4] != 0xAA {
		t.Errorf("data = %v", data[4:])
	}
}

func TestListVars(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "VarA", 0, nil)
	writeEFIVar(t, dir, "VarB", 0, nil)
	r := NewEFIVarReader(dir)
	names, err := r.ListVars()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Errorf("got %d vars, want 2", len(names))
	}
}

func TestIsSecureBootEnabled(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c", 7, []byte{1})
	r := NewEFIVarReader(dir)
	enabled, err := r.IsSecureBootEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("expected SecureBoot enabled")
	}
}

func TestIsSecureBootDisabled(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c", 7, []byte{0})
	r := NewEFIVarReader(dir)
	enabled, err := r.IsSecureBootEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("expected SecureBoot disabled")
	}
}

func TestIsSetupMode(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "SetupMode-8be4df61-93ca-11d2-aa0d-00e098032b8c", 7, []byte{1})
	r := NewEFIVarReader(dir)
	setup, err := r.IsSetupMode()
	if err != nil {
		t.Fatal(err)
	}
	if !setup {
		t.Error("expected setup mode")
	}
}

func TestBuildLoadOption(t *testing.T) {
	entry := BootEntry{Description: "Linux", Loader: "/EFI/BOOT/linux.efi"}
	opt := BuildLoadOption(entry)
	if len(opt) < 6 {
		t.Fatalf("load option too short: %d bytes", len(opt))
	}
	// First 4 bytes should be LOAD_OPTION_ACTIVE (1)
	attrs := binary.LittleEndian.Uint32(opt[:4])
	if attrs != 1 {
		t.Errorf("attrs = %d, want 1", attrs)
	}
}

func TestEncodeUTF16LE(t *testing.T) {
	result := encodeUTF16LE("AB")
	// "AB" + null = 3 UTF-16 code units = 6 bytes
	if len(result) != 6 {
		t.Errorf("length = %d, want 6", len(result))
	}
	if result[0] != 'A' || result[2] != 'B' {
		t.Errorf("encoded = %v", result)
	}
}
