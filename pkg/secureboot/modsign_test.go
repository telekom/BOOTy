package secureboot

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// writeModules creates one module per supported compression below updates
// and extra, and one in-tree module that must stay unsigned.
func writeModules(t *testing.T, root string) map[string][]byte {
	t.Helper()
	plain := map[string][]byte{
		"lib/modules/6.8.0-1/updates/dkms/a.ko":     []byte("\x7fELF module a"),
		"lib/modules/6.8.0-1/updates/dkms/b.ko.zst": []byte("\x7fELF module b"),
		"lib/modules/6.8.0-1/extra/c.ko.xz":         []byte("\x7fELF module c"),
		"lib/modules/6.9.0-2/updates/d.ko.gz":       []byte("\x7fELF module d"),
	}
	for name, raw := range plain {
		data, err := compressModule(name, raw)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, name), data)
	}
	writeFile(t, filepath.Join(root, "lib/modules/6.8.0-1/kernel/fs/intree.ko"), []byte("in-tree"))
	// Not a kernel directory: skipped.
	writeFile(t, filepath.Join(root, "lib/modules/README"), []byte("stray"))
	return plain
}

func writeFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSignModuleTree(t *testing.T) {
	root := t.TempDir()
	plain := writeModules(t, root)

	before, err := os.Stat(filepath.Join(root, "lib/modules/6.8.0-1/updates/dkms/a.ko"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := SignModuleTree(root, "BOOTy test")
	if err != nil {
		t.Fatalf("SignModuleTree: %v", err)
	}
	// Rewritten in place: same inode, so owner and xattrs (SELinux) stay.
	if after, err := os.Stat(filepath.Join(root, "lib/modules/6.8.0-1/updates/dkms/a.ko")); err != nil || !os.SameFile(before, after) {
		t.Fatalf("module file was replaced, not rewritten: %v", err)
	}
	cert, err := x509.ParseCertificate(res.CertDER)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != len(plain) {
		t.Fatalf("signed %v, want the %d out-of-tree modules", res.Modules, len(plain))
	}
	slices.Sort(res.Kernels)
	if !slices.Equal(res.Kernels, []string{"6.8.0-1", "6.9.0-2"}) {
		t.Fatalf("kernels = %v", res.Kernels)
	}
	for name, want := range plain {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		signed, err := decompressModule(name, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		verifyModule(t, name, signed, want, cert)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "lib/modules/6.8.0-1/kernel/fs/intree.ko")); string(got) != "in-tree" {
		t.Fatal("in-tree module was modified")
	}

	// A second run (reprovisioning, resume) replaces the signature.
	res2, err := SignModuleTree(root, "BOOTy test")
	if err != nil {
		t.Fatal(err)
	}
	cert2, _ := x509.ParseCertificate(res2.CertDER)
	data, _ := os.ReadFile(filepath.Join(root, "lib/modules/6.8.0-1/updates/dkms/a.ko"))
	verifyModule(t, "a.ko resigned", data, plain["lib/modules/6.8.0-1/updates/dkms/a.ko"], cert2)
}

func TestSignModuleTreeNoModules(t *testing.T) {
	res, err := SignModuleTree(t.TempDir(), "BOOTy test")
	if err != nil || len(res.Modules) != 0 || len(res.CertDER) == 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// verifyModule checks the sign-file layout and the CMS signature.
func verifyModule(t *testing.T, name string, signed, want []byte, cert *x509.Certificate) {
	t.Helper()
	if !bytes.HasSuffix(signed, []byte(moduleSigMagic)) {
		t.Fatalf("%s: no signature magic", name)
	}
	end := len(signed) - len(moduleSigMagic) - moduleSigTrailerLen
	trailer := signed[end : end+moduleSigTrailerLen]
	if !bytes.Equal(trailer[:8], []byte{0, 0, pkeyIDPKCS7, 0, 0, 0, 0, 0}) {
		t.Fatalf("%s: trailer %x", name, trailer)
	}
	sigLen := int(binary.BigEndian.Uint32(trailer[8:]))
	mod, p7 := signed[:end-sigLen], signed[end-sigLen:end]
	if !bytes.Equal(mod, want) {
		t.Fatalf("%s: module content changed", name)
	}
	var ci contentInfo
	if rest, err := asn1.Unmarshal(p7, &ci); err != nil || len(rest) != 0 {
		t.Fatalf("%s: parse PKCS#7: %v", name, err)
	}
	si := ci.Content.SignerInfos[0]
	if !bytes.Equal(si.SID.Issuer.FullBytes, cert.RawIssuer) || si.SID.Serial.Cmp(cert.SerialNumber) != 0 {
		t.Fatalf("%s: signer is not the certificate", name)
	}
	digest := sha256.Sum256(want)
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], si.Signature); err != nil {
		t.Fatalf("%s: signature: %v", name, err)
	}
	verifyWithOpenSSL(t, mod, p7, cert)
}

// verifyWithOpenSSL checks the signature like the kernel does: a detached
// CMS signature over the module, without signed attributes.
func verifyWithOpenSSL(t *testing.T, mod, p7 []byte, cert *x509.Certificate) {
	t.Helper()
	if _, err := exec.LookPath("openssl"); err != nil {
		return
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mod"), mod)
	writeFile(t, filepath.Join(dir, "sig"), p7)
	writeFile(t, filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	out, err := exec.CommandContext(t.Context(), "openssl", "cms", "-verify", "-binary", "-inform", "DER", "-noverify",
		"-in", filepath.Join(dir, "sig"), "-content", filepath.Join(dir, "mod"),
		"-certfile", filepath.Join(dir, "cert.pem"), "-out", os.DevNull).CombinedOutput()
	if err != nil {
		t.Fatalf("openssl cms -verify: %v: %s", err, out)
	}
}

func TestStripModuleSignature(t *testing.T) {
	if got, err := StripModuleSignature([]byte("plain")); err != nil || string(got) != "plain" {
		t.Fatalf("unsigned: %q %v", got, err)
	}
	if _, err := StripModuleSignature([]byte(moduleSigMagic)); err == nil {
		t.Fatal("truncated signature accepted")
	}
	bogus := append(append([]byte("ab"), 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 99), moduleSigMagic...)
	if _, err := StripModuleSignature(bogus); err == nil {
		t.Fatal("oversized signature accepted")
	}
}

func TestMOKSignatureList(t *testing.T) {
	esl := MOKSignatureList([]byte("cert"))
	if len(esl) != 28+16+4 || binary.LittleEndian.Uint32(esl[16:]) != uint32(len(esl)) ||
		binary.LittleEndian.Uint32(esl[24:]) != 16+4 {
		t.Fatalf("bad list header %x", esl)
	}
	// GUIDs are stored little-endian in their first three fields.
	if !bytes.Equal(esl[:4], []byte{0xa1, 0x59, 0xc0, 0xa5}) || !bytes.Equal(esl[28:32], []byte{0x35, 0x8d, 0x37, 0x11}) {
		t.Fatalf("bad GUIDs %x", esl[:44])
	}
}
