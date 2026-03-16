package secureboot

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
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestReadVar(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "TestVar-abcd-1234", 0x07, []byte{0x42})

	r := NewEFIVarReader(dir)
	data, err := r.ReadVar("TestVar")
	if err != nil {
		t.Fatalf("ReadVar: %v", err)
	}
	if len(data) != 1 || data[0] != 0x42 {
		t.Errorf("data = %v", data)
	}
}

func TestReadVar_NotFound(t *testing.T) {
	dir := t.TempDir()
	r := NewEFIVarReader(dir)
	_, err := r.ReadVar("NoSuchVar")
	if err == nil {
		t.Error("expected error for missing variable")
	}
}

func TestReadVar_TooShort(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Short-1234"), []byte{0, 0}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := NewEFIVarReader(dir)
	_, err := r.ReadVar("Short")
	if err == nil {
		t.Error("expected error for short variable")
	}
}

func TestReadVar_OnlyAttrs(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "EmptyPayload-1234", 0x07, nil)

	r := NewEFIVarReader(dir)
	data, err := r.ReadVar("EmptyPayload")
	if err != nil {
		t.Fatalf("ReadVar: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(data))
	}
}

func TestIsSecureBootEnabled(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c", 0x06, []byte{1})

	r := NewEFIVarReader(dir)
	enabled, err := r.IsSecureBootEnabled()
	if err != nil {
		t.Fatalf("IsSecureBootEnabled: %v", err)
	}
	if !enabled {
		t.Error("expected SecureBoot enabled")
	}
}

func TestIsSecureBootDisabled(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c", 0x06, []byte{0})

	r := NewEFIVarReader(dir)
	enabled, err := r.IsSecureBootEnabled()
	if err != nil {
		t.Fatalf("IsSecureBootEnabled: %v", err)
	}
	if enabled {
		t.Error("expected SecureBoot disabled")
	}
}

func TestIsSetupMode(t *testing.T) {
	dir := t.TempDir()
	writeEFIVar(t, dir, "SetupMode-8be4df61-93ca-11d2-aa0d-00e098032b8c", 0x06, []byte{1})

	r := NewEFIVarReader(dir)
	setup, err := r.IsSetupMode()
	if err != nil {
		t.Fatalf("IsSetupMode: %v", err)
	}
	if !setup {
		t.Error("expected setup mode")
	}
}

func TestWriteVar(t *testing.T) {
	dir := t.TempDir()
	r := NewEFIVarReader(dir)

	err := r.WriteVar("TestWrite-1234", 0x07, []byte{0xAB})
	if err != nil {
		t.Fatalf("WriteVar: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "TestWrite-1234"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("len = %d, want 5", len(data))
	}
	if data[4] != 0xAB {
		t.Errorf("data byte = %x, want AB", data[4])
	}
}

func TestListVars(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct{ name, data string }{
		{"Mok-1234", "a"}, {"MokNew-5678", "b"}, {"Other-9999", "c"},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.data), 0o644); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
	}

	r := NewEFIVarReader(dir)
	vars, err := r.ListVars("Mok")
	if err != nil {
		t.Fatalf("ListVars: %v", err)
	}
	if len(vars) != 2 {
		t.Errorf("got %d Mok vars, want 2", len(vars))
	}
}
