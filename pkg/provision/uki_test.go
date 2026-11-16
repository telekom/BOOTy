//go:build linux

package provision

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/image"
)

// testPE builds a minimal PE32+ image with the named sections, laid out
// as ukify writes it, with room for two more section headers. signed sets
// the size of the security data directory, as an Authenticode signature
// does.
func testPE(tb testing.TB, signed bool, sections ...[2]string) []byte {
	tb.Helper()
	const lfanew, align = 0x40, 0x200
	hdrs := lfanew + 4 + 20 + 240 + 40*(len(sections)+2)
	off := (hdrs + align - 1) / align * align
	var b bytes.Buffer
	b.Write([]byte("MZ"))
	b.Write(make([]byte, 0x3a))
	_ = binary.Write(&b, binary.LittleEndian, uint32(lfanew))
	b.WriteString("PE\x00\x00")
	_ = binary.Write(&b, binary.LittleEndian, struct {
		Machine, NumberOfSections                            uint16
		TimeDateStamp, PointerToSymbolTable, NumberOfSymbols uint32
		SizeOfOptionalHeader, Characteristics                uint16
	}{0x8664, uint16(len(sections)), 0, 0, 0, 240, 0x22})
	oh := make([]byte, 240)
	binary.LittleEndian.PutUint16(oh[0:], 0x20b)
	binary.LittleEndian.PutUint32(oh[32:], align)       // SectionAlignment
	binary.LittleEndian.PutUint32(oh[36:], align)       // FileAlignment
	binary.LittleEndian.PutUint32(oh[60:], uint32(off)) // SizeOfHeaders
	binary.LittleEndian.PutUint32(oh[108:], 16)         // NumberOfRvaAndSizes
	if signed {
		binary.LittleEndian.PutUint32(oh[112+8*4+4:], 0x100) // security directory size
	}
	b.Write(oh)
	data := off
	for _, s := range sections {
		name := make([]byte, 8)
		copy(name, s[0])
		b.Write(name)
		n := uint32(len(s[1]))
		raw := (n + align - 1) / align * align
		_ = binary.Write(&b, binary.LittleEndian, struct {
			VirtualSize, VirtualAddress, SizeOfRawData, PointerToRawData, R1, R2 uint32
			N1, N2                                                               uint16
			Characteristics                                                      uint32
		}{n, uint32(data), raw, uint32(data), 0, 0, 0, 0, 0x40000040})
		data += int(raw)
	}
	b.Write(make([]byte, off-b.Len()))
	for _, s := range sections {
		raw := (len(s[1]) + align - 1) / align * align
		b.WriteString(s[1])
		b.Write(make([]byte, raw-len(s[1])))
	}
	return b.Bytes()
}

func testPublicKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, pub := testPCRKey(t)
	return pub
}

// testPCRKey is a PCR policy key and its PEM public key.
func testPCRKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// testCIPCRSig is the .pcrsig of a ci UKI with .linux k, .cmdline root=x
// and .pcrpkey pub, for the phases from first on.
func testCIPCRSig(t *testing.T, key *rsa.PrivateKey, pub []byte, first int) string {
	t.Helper()
	js, err := ukiPCRSignature(map[string][]byte{"linux": []byte("k"), "cmdline": []byte("root=x"), "pcrpkey": pub}, key)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string][]ukiPCRSigEntry
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatal(err)
	}
	doc["sha256"] = doc["sha256"][first:]
	js, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(js)
}

func TestUKISections(t *testing.T) {
	secs, signed, err := ukiSections(testPE(t, false, [2]string{".text", "stub"}, [2]string{".linux", "kernel"}, [2]string{".cmdline", "root=x"}))
	if err != nil || signed {
		t.Fatalf("signed=%v err=%v", signed, err)
	}
	if string(secs["linux"]) != "kernel" || string(secs["cmdline"]) != "root=x" {
		t.Fatalf("sections not cut to their virtual size: %q %q", secs["linux"], secs["cmdline"])
	}
	if _, signed, _ := ukiSections(testPE(t, true, [2]string{".linux", "k"})); !signed {
		t.Fatal("the security directory was not seen")
	}
	for name, pe := range map[string][]byte{
		"no .linux":     testPE(t, false, [2]string{".text", "x"}),
		"multi-profile": testPE(t, false, [2]string{".linux", "k"}, [2]string{".profile", "ID=a"}),
		"not PE":        []byte("MZ nonsense"),
	} {
		if _, _, err := ukiSections(pe); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// ukiTestHost is a target root with the UKI tools and an ESP with uki.
func ukiTestHost(t *testing.T, signing string, uki []byte) (*Orchestrator, *mockCommander, string, string) {
	t.Helper()
	root := encryptionTargetRoot(t)
	for _, rel := range ukiTargetTools {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	esp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(esp, "EFI/Linux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi"), uki, 0o644); err != nil {
		t.Fatal(err)
	}
	old := isMountPoint
	isMountPoint = func(string) bool { return true }
	t.Cleanup(func() { isMountPoint = old })
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption = config.DiskEncryptionConfig{Mode: config.DiskEncryptionLUKS2TPM2UKI, RecoveryKeyURL: "https://x"}
	cfg.Provision.SecureBoot.Signing = signing
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	sum := sha256.Sum256(uki)
	o.ukiDigest = hex.EncodeToString(sum[:])
	return o, cmd, root, esp
}

// countKeyGeneration fails the test if fail is set and a key is generated.
func countKeyGeneration(t *testing.T, fail bool) *int {
	t.Helper()
	n := 0
	old := newUKISigningKey
	newUKISigningKey = func() (*rsa.PrivateKey, error) {
		n++
		if fail {
			t.Error("SECUREBOOT_SIGNING=ci generated a private key")
		}
		return rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	}
	t.Cleanup(func() { newUKISigningKey = old })
	return &n
}

// TestPrepareUKICIMakesNoKey: with SECUREBOOT_SIGNING=ci BOOTy verifies the
// signed UKI and installs its public material; no private key exists.
func TestPrepareUKICIMakesNoKey(t *testing.T) {
	countKeyGeneration(t, true)
	key, pub := testPCRKey(t)
	sig := testCIPCRSig(t, key, pub, 0)
	uki := testPE(t, true, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", string(pub)}, [2]string{".pcrsig", sig})
	o, cmd, root, esp := ukiTestHost(t, config.SigningCI, uki)

	pinned := filepath.Join(t.TempDir(), "pcr.pem")
	if err := os.WriteFile(pinned, pub, 0o644); err != nil {
		t.Fatal(err)
	}
	o.cfg.Provision.DiskEncryption.PCRPublicKeyFile = pinned
	if err := o.loadPCRPin(); err != nil {
		t.Fatal(err)
	}
	// The pin was read before the disk was written; a later change of the
	// file (a deploy archive swaps /deploy) does not matter.
	if err := os.Remove(pinned); err != nil {
		t.Fatal(err)
	}
	st, err := o.prepareUKI(root, esp)
	if err != nil {
		t.Fatal(err)
	}
	if st.loader != `\EFI\Linux\ubuntu-6.8.efi` || st.cert != nil || len(cmd.calls) != 0 {
		t.Fatalf("state %+v, commands %v", st, cmd.calls)
	}
	for p, want := range map[string]string{ukiPCRPublicKeyPath: string(pub), ukiPCRSignaturePath: sig} {
		if got, _ := os.ReadFile(filepath.Join(root, p)); string(got) != want {
			t.Errorf("%s = %q", p, got)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi")); !bytes.Equal(got, uki) {
		t.Fatal("ci mode changed the UKI")
	}

	// Another pinned key, an unsigned UKI, a UKI without .pcrsig, with a
	// .pcrsig only for the userspace phases or by another key, without a
	// command line, or a digest that the manifest does not name all fail.
	if err := os.WriteFile(pinned, testPublicKeyPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.loadPCRPin(); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "not the key in") {
		t.Fatalf("other pinned key: %v", err)
	}
	o.cfg.Provision.DiskEncryption.PCRPublicKeyFile = ""
	other, _ := testPCRKey(t)
	for name, bad := range map[string][]byte{
		"unsigned":  testPE(t, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", string(pub)}, [2]string{".pcrsig", sig}),
		"no pcrsig": testPE(t, true, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", string(pub)}),
		"userspace phases only": testPE(t, true, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", string(pub)},
			[2]string{".pcrsig", testCIPCRSig(t, key, pub, 1)}),
		"signed by another key": testPE(t, true, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", string(pub)},
			[2]string{".pcrsig", testCIPCRSig(t, other, pub, 0)}),
		"policy of another UKI": testPE(t, true, [2]string{".linux", "k2"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", string(pub)},
			[2]string{".pcrsig", sig}),
	} {
		o, _, root, esp := ukiTestHost(t, config.SigningCI, bad)
		if _, err := o.prepareUKI(root, esp); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// systemd-stub takes the command line of the boot entry, measured into
	// the unbound PCR 12, when the UKI has none.
	for name, cmdline := range map[string][]string{"no cmdline": nil, "blank cmdline": {".cmdline", " \n\x00"}} {
		secs := [][2]string{{".linux", "k"}, {".pcrpkey", string(pub)}, {".pcrsig", sig}}
		if cmdline != nil {
			secs = append(secs, [2]string{cmdline[0], cmdline[1]})
		}
		o, _, root, esp := ukiTestHost(t, config.SigningCI, testPE(t, true, secs...))
		if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "no .cmdline") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	o.ukiDigest = strings.Repeat("0", 64)
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "signed image manifest says") {
		t.Fatalf("wrong digest: %v", err)
	}
	o.ukiDigest = ""
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), UKIDigestAnnotation) {
		t.Fatalf("no digest: %v", err)
	}
}

// TestPrepareUKIPerHost: per-host signing makes two keys, signs in BOOTy
// and runs no program of the image, which could read the keys (#944
// review; #935 for mokutil). The signed UKI carries the PCR policy key and
// signature that go to the target, and the original sections unchanged.
func TestPrepareUKIPerHost(t *testing.T) {
	n := countKeyGeneration(t, false)
	uki := testPE(t, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".osrel", "ID=x"})
	o, cmd, root, esp := ukiTestHost(t, config.SigningPerHost, uki)
	st, err := o.prepareUKI(root, esp)
	if err != nil {
		t.Fatal(err)
	}
	if *n != 2 {
		t.Fatalf("%d keys generated, want the PCR policy key and the Secure Boot key", *n)
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("per-host signing ran %v while the private keys existed", cmd.calls)
	}
	signed, err := os.ReadFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi"))
	if err != nil {
		t.Fatal(err)
	}
	secs, ok, err := ukiSections(signed)
	if err != nil || !ok {
		t.Fatalf("signed UKI: signed=%v err=%v", ok, err)
	}
	pub, _ := os.ReadFile(filepath.Join(root, ukiPCRPublicKeyPath))
	sig, _ := os.ReadFile(filepath.Join(root, ukiPCRSignaturePath))
	if !bytes.Equal(secs["pcrpkey"], pub) || !bytes.Equal(secs["pcrsig"], sig) || string(secs["cmdline"]) != "root=x" || string(secs["linux"]) != "k" {
		t.Fatalf("sections %q", secs)
	}
	if sum := sha256.Sum256(signed); hex.EncodeToString(sum[:]) != st.sha256 || st.cert == nil {
		t.Fatalf("state %+v", st)
	}
	cert, _ := os.ReadFile(filepath.Join(esp, ukiCertESPPath))
	if b, _ := pem.Decode(cert); b == nil || !bytes.Equal(b.Bytes, st.cert) || bytes.Contains(signed, []byte("PRIVATE KEY")) {
		t.Fatal("the ESP lacks the certificate, or a private key is in the UKI")
	}

	// An already signed UKI or one with .pcrpkey is refused before any key
	// is made.
	*n = 0
	for _, bad := range [][]byte{
		testPE(t, true, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}),
		testPE(t, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".pcrpkey", "x"}),
	} {
		o, _, root, esp = ukiTestHost(t, config.SigningPerHost, bad)
		if _, err := o.prepareUKI(root, esp); err == nil || *n != 0 {
			t.Fatalf("err=%v keys=%d", err, *n)
		}
	}
}

// TestUKIPCRSignature: the .pcrsig is what systemd-measure 259 signs for
// the same sections (pol from `systemd-measure sign --linux=... --bank=sha256`
// with these contents, all ten measured sections), and each signature
// verifies with the key.
func TestUKIPCRSignature(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		t.Fatal(err)
	}
	secs := map[string][]byte{"linux": []byte("kernel"), "osrel": []byte("ID=x\n"), "cmdline": []byte("root=x"), "initrd": []byte("initrd"),
		"ucode": []byte("ucode"), "splash": []byte("BM"), "dtb": []byte("dtb"), "uname": []byte("6.8.0"), "sbat": []byte("sbat,1\n"), "pcrpkey": []byte("pub")}
	js, err := ukiPCRSignature(secs, key)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SHA256 []struct {
			PCRs           []int
			PKFP, Pol, Sig string
		}
	}
	if err := json.Unmarshal(js, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"e9b0d22b89a2085ad95e123063a3d2e1681c86a88bd17858d09522da4b06dec7", "84ac7f41088c863a62e23f3c857c388a22da9fbf00d78619a60c9936d05cee9e",
		"69ab1c62f6444c6c5057218219843da8b0d36dd970f244686ed0f90368688afd", "78b11b8dac5dab4b836f94c34f1aa3054ffaf9f89cecbae52e4bccf089c4d687"}
	fp := sha256.Sum256(x509.MarshalPKCS1PublicKey(&key.PublicKey))
	if len(got.SHA256) != len(want) {
		t.Fatalf("%s", js)
	}
	for i, e := range got.SHA256 {
		pol, _ := hex.DecodeString(e.Pol)
		sig, _ := base64.StdEncoding.DecodeString(e.Sig)
		h := sha256.Sum256(pol)
		if e.Pol != want[i] || e.PKFP != hex.EncodeToString(fp[:]) || len(e.PCRs) != 1 || e.PCRs[0] != 11 ||
			rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, h[:], sig) != nil {
			t.Errorf("phase %d: %+v", i, e)
		}
	}
}

// TestAuthenticodeSign: the hash is the one osslsigncode 2.x calculates for
// this file, the result is signed, and layouts that the hash would not
// cover are refused.
func TestAuthenticodeSign(t *testing.T) {
	uki := testPE(t, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"})
	out, err := appendPESections(uki, [2][]byte{[]byte(".pcrpkey"), []byte("pub")}, [2][]byte{[]byte(".pcrsig"), []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	l, err := parsePELayout(out)
	if err != nil {
		t.Fatal(err)
	}
	if d := authenticodeDigest(out, l); hex.EncodeToString(d[:]) != "cfe7f03e19f6940e8c3488bef575d8f90348a44390ef0cda95d87895d66975e9" {
		t.Errorf("Authenticode digest %x", d)
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		t.Fatal(err)
	}
	der, err := ukiCertificate(key, "test")
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	signed, err := authenticodeSign(out, key, cert)
	if err != nil {
		t.Fatal(err)
	}
	secs, ok, err := ukiSections(signed)
	if err != nil || !ok || string(secs["pcrpkey"]) != "pub" || string(secs["pcrsig"]) != "{}" || !bytes.HasPrefix(signed, out[:0x80]) {
		t.Fatalf("signed=%v err=%v sections %q", ok, err, secs)
	}
	if _, err := authenticodeSign(signed, key, cert); err == nil {
		t.Error("signed twice")
	}
	if _, err := appendPESections(append(slices.Clone(uki), 1), [2][]byte{[]byte(".x"), nil}); err == nil {
		t.Error("trailing data accepted")
	}
	// Header values that would pad the added sections to GiBs or wrap
	// their addresses are refused.
	for name, mod := range map[string]func(b []byte){
		"FileAlignment 2 GiB":        func(b []byte) { binary.LittleEndian.PutUint32(b[0x58+36:], 0x80000000) },
		"SectionAlignment not pow 2": func(b []byte) { binary.LittleEndian.PutUint32(b[0x58+32:], 0x300) },
		"sections end at 4 GiB":      func(b []byte) { binary.LittleEndian.PutUint32(b[0x148+12:], 0xfffffe00) },
	} {
		b := slices.Clone(uki)
		mod(b)
		if _, err := appendPESections(b, [2][]byte{[]byte(".pcrpkey"), []byte("pub")}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// FuzzUKISign: replacePESection, appendPESections and authenticodeSign error on malformed
// PE input, never panic or allocate beyond the input.
func FuzzUKISign(f *testing.F) {
	f.Add(testPE(f, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}))
	f.Add(testPE(f, false, [2]string{".linux", strings.Repeat("k", 0x300)}))
	// SizeOfHeaders ends inside the section table: section data overlaps it.
	overlap := testPE(f, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"})
	binary.LittleEndian.PutUint32(overlap[0x58+60:], 0x100)
	f.Add(overlap)
	key, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		f.Fatal(err)
	}
	der, err := ukiCertificate(key, "fuzz")
	if err != nil {
		f.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	f.Fuzz(func(t *testing.T, b []byte) {
		if out, err := replacePESection(b, ".cmdline", []byte("root=x booty.test=1")); err == nil {
			if _, _, err := parseUnsignedPE(out); err != nil || len(out) > len(b)+0x10000 {
				t.Fatalf("replacePESection wrote %d bytes from %d: %v", len(out), len(b), err)
			}
		}
		out, err := appendPESections(b, [2][]byte{[]byte(".pcrpkey"), []byte("pub")}, [2][]byte{[]byte(".pcrsig"), []byte("{}")})
		if err != nil {
			return
		}
		if len(out) > len(b)+2*0x10000 { // two sections padded to the largest FileAlignment
			t.Fatalf("%d bytes from %d", len(out), len(b))
		}
		signed, err := authenticodeSign(out, key, cert)
		if err != nil {
			t.Fatalf("sign the PE that appendPESections wrote: %v", err)
		}
		if _, _, err := ukiSections(b); err == nil {
			if _, _, err := ukiSections(signed); err != nil {
				t.Fatalf("the signed UKI does not parse: %v", err)
			}
		}
	})
}

func TestPrepareUKINeedsOneUKI(t *testing.T) {
	o, _, root, esp := ukiTestHost(t, config.SigningCI, testPE(t, true, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}))
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/other.efi"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "exactly one UKI") {
		t.Fatalf("err = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "usr/bin/systemd-cryptenroll")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "systemd-cryptenroll") {
		t.Fatalf("missing tool: %v", err)
	}
}

func TestDestroyRSAKey(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		t.Fatal(err)
	}
	destroyRSAKey(k)
	if k.D.Sign() != 0 || k.Primes[0].Sign() != 0 {
		t.Fatal("the key was not zeroed")
	}
}

func TestUKIEnrollScript(t *testing.T) {
	root := t.TempDir()
	st := &encryptionState{luksUUID: "u-1", enrollKey: []byte{}}
	if err := writeUKIEnrollFiles(root, st); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(root, luksEnvPath))
	for _, want := range []string{"TPM2_PCRS='7'\n", "TPM2_PUBLIC_KEY='" + ukiPCRPublicKeyPath, "TPM2_SIGNATURE='" + ukiPCRSignaturePath} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env lacks %q", want)
		}
	}
	unit, _ := os.ReadFile(filepath.Join(root, "etc/systemd/system", luksUnitName))
	if !strings.Contains(string(unit), "ConditionPathExists="+ukiPCRSignaturePath) {
		t.Errorf("unit: %s", unit)
	}
	// The empty passphrase is wiped only after the checked enrollment. A
	// rerun after the wipe (interrupted before the unit was disabled) finds
	// the TPM2 token and finishes without the empty passphrase.
	last := -1
	for _, s := range []string{"grep -q systemd-tpm2", "systemctl disable", "--unlock-key-file=/dev/null", "--tpm2-public-key-pcrs=11",
		"--tpm2-signature=", "--tpm2-pcrs=", "luksKillSlot"} {
		i := strings.Index(ukiEnrollScript, s)
		if i <= last {
			t.Fatalf("%q missing or out of order", s)
		}
		last = i
	}
	if strings.LastIndex(ukiEnrollScript, "systemctl disable") < last {
		t.Fatal("the unit is not disabled after the wipe")
	}
	for _, f := range []string{"etc/crypttab", luksDracutConf, luksDracutKeyConf, luksEnrollKeyPath} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("UKI mode wrote %s; crypttab and the initramfs belong to the UKI", f)
		}
	}
}

// TestUKIStepsOffByDefault: without luks2-tpm2-uki nothing UKI-related
// runs, so a plain provisioning is unchanged.
func TestUKIStepsOffByDefault(t *testing.T) {
	countKeyGeneration(t, true)
	old := ociManifestAnnotation
	ociManifestAnnotation = func(context.Context, string, string) (string, error) {
		t.Error("annotation fetched without the UKI mode")
		return "", nil
	}
	t.Cleanup(func() { ociManifestAnnotation = old })
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	if err := o.loadUKIDigest(context.Background(), "oci://r/i@sha256:"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := o.prepareDiskEncryption(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.createUKIBootEntry(context.Background()); err != nil || len(cmd.calls) != 0 || o.reportUKI() != nil {
		t.Fatalf("err=%v calls=%v", err, cmd.calls)
	}
}

func TestUKIBootEntryStep(t *testing.T) {
	names := func(mode string) []string {
		cfg := &config.MachineConfig{}
		cfg.Provision.DiskEncryption.Mode = mode
		steps := newTestOrchestrator(t, cfg, &mockProvider{}).provisionSteps()
		n := make([]string, 0, len(steps))
		for _, s := range steps {
			n = append(n, s.Name)
		}
		return n
	}
	uki := strings.Join(names(config.DiskEncryptionLUKS2TPM2UKI), " ")
	if !strings.Contains(uki, "create-mirror-efi-boot-entry enroll-module-signing-key create-uki-boot-entry teardown-chroot") {
		t.Fatalf("steps: %s", uki)
	}
	for _, mode := range []string{"", config.DiskEncryptionLUKS2TPM2} {
		if slices.Contains(names(mode), "create-uki-boot-entry") {
			t.Fatalf("mode %q has the UKI boot entry step", mode)
		}
	}
}

func TestCreateUKIBootEntry(t *testing.T) {
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	o.targetDisk, o.bootPartition = "/dev/vda", "/dev/vda15"
	esp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(esp, "EFI/Linux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/u.efi"), []byte("uki"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("uki"))
	o.encryption = &encryptionState{uki: &ukiState{loader: `\EFI\Linux\u.efi`, esp: esp, rel: "EFI/Linux/u.efi", sha256: hex.EncodeToString(sum[:])}}
	cmd.setResult("efibootmgr", []byte("BootOrder: 0003,0001\nBoot0001* ubuntu\tHD()\nBoot0003* booty-uki\tHD()\n"), nil)
	if err := o.createUKIBootEntry(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(cmd.calls))
	for _, c := range cmd.calls {
		got = append(got, strings.Join(c.args, " "))
	}
	want := []string{"", "-b 0003 -B", `-c -d /dev/vda -p 15 -L booty-uki -l \EFI\Linux\u.efi`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("efibootmgr calls %q, want %q", got, want)
	}

	// Image code ran in the chroot after prepare-disk-encryption: a UKI
	// that changed since is not booted.
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/u.efi"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd.calls = nil
	if err := o.createUKIBootEntry(context.Background()); err == nil || !strings.Contains(err.Error(), "changed after") || len(cmd.calls) != 0 {
		t.Fatalf("changed UKI: err=%v calls=%v", err, cmd.calls)
	}
}

// TestLoadUKIDigest: verify-image, before wipe-disks, fails on a missing
// or malformed annotation unless DISK_ENCRYPTION_ALLOW_FAILURE leaves the
// fallback to the stream-image preflight.
func TestLoadUKIDigest(t *testing.T) {
	value := ""
	old := ociManifestAnnotation
	ociManifestAnnotation = func(context.Context, string, string) (string, error) { return value, nil }
	t.Cleanup(func() { ociManifestAnnotation = old })
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption.Mode = config.DiskEncryptionLUKS2TPM2UKI
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	for _, v := range []string{"", strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		value = v
		if err := o.loadUKIDigest(context.Background(), "oci://r/i"); err == nil || o.ukiDigest != "" {
			t.Errorf("annotation %q: err=%v digest=%q", v, err, o.ukiDigest)
		}
	}
	value = strings.Repeat("a", 64)
	if err := o.loadUKIDigest(context.Background(), ""); err == nil || o.ukiDigest != "" {
		t.Errorf("no verified OCI manifest (HTTP image): err=%v digest=%q", err, o.ukiDigest)
	}
	cfg.Provision.DiskEncryption.PCRPublicKeyFile = filepath.Join(t.TempDir(), "missing.pem")
	if err := o.loadUKIDigest(context.Background(), "oci://r/i"); err == nil || !strings.Contains(err.Error(), "DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE") {
		t.Errorf("missing pin file: err=%v", err)
	}
	cfg.Provision.DiskEncryption.PCRPublicKeyFile = ""
	o.ukiDigest, value = "", ""
	cfg.Provision.DiskEncryption.AllowFailure = true
	if err := o.loadUKIDigest(context.Background(), "oci://r/i"); err != nil || o.ukiDigest != "" {
		t.Errorf("allow failure: err=%v digest=%q", err, o.ukiDigest)
	}
	value = " " + strings.Repeat("A", 64) + "\n"
	if err := o.loadUKIDigest(context.Background(), "oci://r/i"); err != nil || o.ukiDigest != strings.Repeat("a", 64) {
		t.Errorf("valid: err=%v digest=%q", err, o.ukiDigest)
	}
}

// TestCheckEncryptedRootUKILVM: the UKI mode refuses an LVM physical
// volume before the first write, as an unsupported layout, so
// DISK_ENCRYPTION_ALLOW_FAILURE can still fall back.
func TestCheckEncryptedRootUKILVM(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption.Mode = config.DiskEncryptionLUKS2TPM2UKI
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.checkEncryptedRoot(true); !errors.Is(err, image.ErrEncryptedRootUnsupported) {
		t.Fatalf("err = %v, want ErrEncryptedRootUnsupported", err)
	}
	if err := o.checkEncryptedRoot(false); err != nil {
		t.Fatal(err)
	}
}
