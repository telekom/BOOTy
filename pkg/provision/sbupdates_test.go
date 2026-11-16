//go:build linux

package provision

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/efi"
)

// efiCertSHA256 is EFI_CERT_SHA256_GUID in its byte order on disk.
var efiCertSHA256 = []byte{0x26, 0x16, 0xc4, 0xc1, 0x4c, 0x50, 0x92, 0x40, 0xac, 0xa9, 0x41, 0xf9, 0x36, 0x93, 0x43, 0x28}

// testESL is one EFI_SIGNATURE_LIST of SHA-256 entries with the owner o.
func testESL(o byte, hashes ...byte) []byte {
	const sigSize = 16 + 32
	b := make([]byte, 28, 28+len(hashes)*sigSize)
	copy(b, efiCertSHA256)
	binary.LittleEndian.PutUint32(b[16:], uint32(28+len(hashes)*sigSize)) //nolint:gosec // tiny test sizes
	binary.LittleEndian.PutUint32(b[24:], sigSize)
	for _, h := range hashes {
		b = append(b, bytes.Repeat([]byte{o}, 16)...)
		b = append(b, bytes.Repeat([]byte{h}, 32)...)
	}
	return b
}

// testAuthVar wraps lists in an EFI_VARIABLE_AUTHENTICATION_2 header with a
// dummy PKCS7 blob, as in dbxupdate_x64.bin.
func testAuthVar(lists ...[]byte) []byte {
	sig := []byte("pkcs7 signed data")
	b := make([]byte, 16+24, 16+24+len(sig))
	binary.LittleEndian.PutUint32(b[16:], uint32(24+len(sig))) //nolint:gosec // tiny test sizes
	binary.LittleEndian.PutUint16(b[20:], 0x0200)
	binary.LittleEndian.PutUint16(b[22:], 0x0EF1)
	copy(b[24:], efiCertTypePKCS7)
	b = append(b, sig...)
	return append(b, bytes.Join(lists, nil)...)
}

func TestSignatureEntries(t *testing.T) {
	got, err := signatureEntries(append(testESL(1, 0xa, 0xb), testESL(2, 0xc)...))
	if err != nil || len(got) != 3 {
		t.Fatalf("signatureEntries = %d entries, %v", len(got), err)
	}
	// The owner is not part of an entry.
	again, _ := signatureEntries(testESL(9, 0xa))
	if got[0] != again[0] || !strings.HasPrefix(got[0], string(efiCertSHA256)) || len(got[0]) != 16+32 {
		t.Fatalf("entry %x", got[0])
	}
	if got, err := signatureEntries(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty variable: %v %v", got, err)
	}
	bad := testESL(1, 0xa)
	for name, b := range map[string][]byte{
		"truncated header":  bad[:20],
		"size past the end": bad[:len(bad)-1],
		"partial signature": func() []byte {
			c := bytes.Clone(bad)
			binary.LittleEndian.PutUint32(c[24:], 40)
			return c
		}(),
	} {
		if _, err := signatureEntries(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestAuthVarSignatures(t *testing.T) {
	got, err := authVarSignatures(testAuthVar(testESL(1, 0xa, 0xb)))
	if err != nil || len(got) != 2 {
		t.Fatalf("authVarSignatures = %d, %v", len(got), err)
	}
	upd := testAuthVar(testESL(1, 0xa))
	wrongType := bytes.Clone(upd)
	wrongType[24] ^= 0xff
	tooLong := bytes.Clone(upd)
	binary.LittleEndian.PutUint32(tooLong[16:], uint32(len(upd))) //nolint:gosec // tiny test sizes
	for name, b := range map[string][]byte{"short": upd[:30], "cert type": wrongType, "cert length": tooLong, "bare list": testESL(1, 0xa)} {
		if _, err := authVarSignatures(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// sbUpdateFixture is an image update tree and a fake efivarfs.
type sbUpdateFixture struct {
	dir, vars string
	log       bytes.Buffer
}

func newSBUpdateFixture(t *testing.T) *sbUpdateFixture {
	t.Helper()
	f := &sbUpdateFixture{dir: t.TempDir(), vars: t.TempDir()}
	return f
}

func (f *sbUpdateFixture) update(t *testing.T, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(f.dir, sbUpdatesDir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// firmware sets a variable of the fake efivarfs: attributes, then data.
func (f *sbUpdateFixture) firmware(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.vars, name), append([]byte{7, 0, 0, 0}, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *sbUpdateFixture) apply(t *testing.T) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(&f.log, nil))
	if err := applySecureBootUpdateFiles(log, os.DirFS(f.dir), efi.NewEFIVarReader(f.vars)); err != nil {
		t.Fatalf("applySecureBootUpdateFiles: %v", err)
	}
}

const testDBX = "dbx-" + efiImageSecurityDatabaseGUID

func TestApplySecureBootUpdatesAppendsMissing(t *testing.T) {
	f := newSBUpdateFixture(t)
	upd := testAuthVar(testESL(1, 0xa, 0xb))
	f.update(t, "dbx/dbxupdate_x64.bin", upd)
	f.firmware(t, testDBX, testESL(1, 0xa))
	f.apply(t)
	got, err := os.ReadFile(filepath.Join(f.vars, testDBX))
	if err != nil {
		t.Fatal(err)
	}
	// The write is what sbkeysync writes: append attributes, then the whole
	// signed update, in one write.
	want := append([]byte{0x67, 0, 0, 0}, upd...)
	if !bytes.HasPrefix(got, want) {
		t.Fatalf("dbx write = %x, want %x", got, want)
	}
	if !strings.Contains(f.log.String(), `msg="Secure Boot update applied" variable=dbx file=`) ||
		!strings.Contains(f.log.String(), "entries=2 new=1") {
		t.Fatalf("log: %s", f.log.String())
	}
}

func TestApplySecureBootUpdatesSkipsPresent(t *testing.T) {
	f := newSBUpdateFixture(t)
	f.update(t, "dbx/dbxupdate_x64.bin", testAuthVar(testESL(1, 0xa, 0xb)))
	// Present under another owner and spread over two lists.
	fw := append(testESL(2, 0xb), testESL(3, 0xc, 0xa)...)
	f.firmware(t, testDBX, fw)
	f.update(t, "dbx/.hidden.bin", testAuthVar(testESL(1, 0xd)))
	f.apply(t)
	got, _ := os.ReadFile(filepath.Join(f.vars, testDBX))
	if !bytes.Equal(got[4:], fw) {
		t.Fatal("dbx written although every entry is present")
	}
	if !strings.Contains(f.log.String(), `msg="Secure Boot update already applied" variable=dbx`) || strings.Contains(f.log.String(), "hidden") {
		t.Fatalf("log: %s", f.log.String())
	}
}

func TestApplySecureBootUpdatesOrderAndMissingVariable(t *testing.T) {
	f := newSBUpdateFixture(t)
	f.update(t, "dbx/b.bin", testAuthVar(testESL(1, 0xa)))
	f.update(t, "dbx/a.bin", []byte("skipped, logged"))
	f.update(t, "db/db.auth", testAuthVar(testESL(1, 0xc)))
	f.update(t, "KEK/kek.auth", testAuthVar(testESL(1, 0xd)))
	f.update(t, "PK/pk.auth", testAuthVar(testESL(1, 0xe)))
	f.apply(t)
	// No variable existed: each is created.
	for _, name := range []string{"KEK-" + efiGlobalGUID, "db-" + efiImageSecurityDatabaseGUID, testDBX} {
		if _, err := os.Stat(filepath.Join(f.vars, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.vars, "PK-"+efiGlobalGUID)); err == nil {
		t.Error("PK written")
	}
	var order []string
	for line := range strings.Lines(f.log.String()) {
		if i := strings.Index(line, "file="); i >= 0 {
			order = append(order, filepath.Base(strings.Fields(line[i:])[0]))
		}
	}
	if strings.Join(order, " ") != "kek.auth db.auth a.bin b.bin" {
		t.Fatalf("order %v", order)
	}
}

func TestApplySecureBootUpdatesRefusalAndBadFileWarn(t *testing.T) {
	f := newSBUpdateFixture(t)
	f.update(t, "dbx/bad.bin", []byte("not an update"))
	f.update(t, "db/db.auth", testAuthVar(testESL(1, 0xc)))
	if err := os.Mkdir(filepath.Join(f.vars, "db-"+efiImageSecurityDatabaseGUID), 0o755); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(&f.log, nil))
	err := applySecureBootUpdateFiles(log, os.DirFS(f.dir), efi.NewEFIVarReader(f.vars))
	// Reading the variable fails first: BOOTy cannot tell what is present.
	if err == nil || !strings.Contains(err.Error(), "read db") {
		t.Fatalf("unreadable db: %v", err)
	}
	// A dangling link reads as a missing variable, and the write fails like
	// a refusal of the firmware (EACCES, EINVAL from efivarfs), also as root.
	dbVar := filepath.Join(f.vars, "db-"+efiImageSecurityDatabaseGUID)
	if err := os.Remove(dbVar); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.vars, "missing", "db"), dbVar); err != nil {
		t.Fatal(err)
	}
	f.log.Reset()
	f.apply(t)
	for _, want := range []string{`level=WARN msg="the firmware refused the Secure Boot update; secureboot-db of the image gets the same answer, so PCR 7 stays" variable=db`,
		`level=WARN msg="Secure Boot update skipped: not a signed signature list" variable=dbx`} {
		if !strings.Contains(f.log.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, f.log.String())
		}
	}
}

func TestApplySecureBootUpdatesStepGates(t *testing.T) {
	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	// Without encryption, or turned off, the step opens no /newroot.
	if err := o.applySecureBootUpdates(context.Background()); err != nil {
		t.Fatal(err)
	}
	off := false
	cfg.Provision.SecureBoot.ApplyDBXUpdates = &off
	o.encryption = &encryptionState{}
	if err := o.applySecureBootUpdates(context.Background()); err != nil {
		t.Fatalf("SECUREBOOT_APPLY_DBX_UPDATES=false: %v", err)
	}
}
