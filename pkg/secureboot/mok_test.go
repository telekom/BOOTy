package secureboot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnrollMOK(t *testing.T) {
	dir := t.TempDir()
	r := NewEFIVarReader(dir)
	e := NewMOKEnroller(nil, r)

	certDER := []byte{0x30, 0x82, 0x01, 0x22} // fake DER cert prefix
	err := e.EnrollMOK(certDER)
	if err != nil {
		t.Fatalf("EnrollMOK: %v", err)
	}

	// Verify MokNew variable was written.
	path := filepath.Join(dir, "MokNew-"+mokNewGUID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MokNew: %v", err)
	}
	// 4 bytes attrs + cert data.
	if len(data) != 4+len(certDER) {
		t.Errorf("MokNew size = %d, want %d", len(data), 4+len(certDER))
	}
}

func TestEnrollMOK_Empty(t *testing.T) {
	dir := t.TempDir()
	r := NewEFIVarReader(dir)
	e := NewMOKEnroller(nil, r)

	err := e.EnrollMOK(nil)
	if err == nil {
		t.Error("expected error for empty cert")
	}
}

func TestListMOKs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "MokListRT-1234"), []byte("x"), 0o644)

	r := NewEFIVarReader(dir)
	e := NewMOKEnroller(nil, r)

	moks, err := e.ListMOKs()
	if err != nil {
		t.Fatalf("ListMOKs: %v", err)
	}
	if len(moks) != 1 {
		t.Errorf("got %d MOKs, want 1", len(moks))
	}
}
