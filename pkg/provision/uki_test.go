//go:build linux

package provision

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

// testPE builds a minimal PE32+ image with the named sections. signed sets
// the size of the security data directory, as an Authenticode signature
// does.
func testPE(t *testing.T, signed bool, sections ...[2]string) []byte {
	t.Helper()
	const lfanew, align = 0x40, 0x200
	hdrs := lfanew + 4 + 20 + 240 + 40*len(sections)
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
	binary.LittleEndian.PutUint32(oh[108:], 16) // NumberOfRvaAndSizes
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
	k, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
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
	for _, rel := range append(append([]string(nil), ukiTargetTools...), ukiPerHostTools...) {
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
	old, oldWork := isMountPoint, ukiWorkDir
	isMountPoint = func(string) bool { return true }
	ukiWorkDir = filepath.Join(t.TempDir(), "work")
	t.Cleanup(func() { isMountPoint, ukiWorkDir = old, oldWork })
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
	pub := testPublicKeyPEM(t)
	sig := `{"sha256":[{"pcrs":[11],"sig":"x"}]}`
	uki := testPE(t, true, [2]string{".linux", "k"}, [2]string{".pcrpkey", string(pub)}, [2]string{".pcrsig", sig})
	o, cmd, root, esp := ukiTestHost(t, config.SecureBootSigningCI, uki)

	pinned := filepath.Join(t.TempDir(), "pcr.pem")
	if err := os.WriteFile(pinned, pub, 0o644); err != nil {
		t.Fatal(err)
	}
	o.cfg.Provision.DiskEncryption.PCRPublicKeyFile = pinned
	st, err := o.prepareUKI(context.Background(), root, esp)
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

	// Another pinned key, an unsigned UKI, a UKI without .pcrsig, or a
	// digest that the manifest does not name all fail.
	if err := os.WriteFile(pinned, testPublicKeyPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(context.Background(), root, esp); err == nil || !strings.Contains(err.Error(), "not the key in") {
		t.Fatalf("other pinned key: %v", err)
	}
	o.cfg.Provision.DiskEncryption.PCRPublicKeyFile = ""
	for name, bad := range map[string][]byte{
		"unsigned":  testPE(t, false, [2]string{".linux", "k"}, [2]string{".pcrpkey", string(pub)}, [2]string{".pcrsig", sig}),
		"no pcrsig": testPE(t, true, [2]string{".linux", "k"}, [2]string{".pcrpkey", string(pub)}),
	} {
		o, _, root, esp := ukiTestHost(t, config.SecureBootSigningCI, bad)
		if _, err := o.prepareUKI(context.Background(), root, esp); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	o.ukiDigest = strings.Repeat("0", 64)
	if _, err := o.prepareUKI(context.Background(), root, esp); err == nil || !strings.Contains(err.Error(), "signed image manifest says") {
		t.Fatalf("wrong digest: %v", err)
	}
	o.ukiDigest = ""
	if _, err := o.prepareUKI(context.Background(), root, esp); err == nil || !strings.Contains(err.Error(), UKIDigestAnnotation) {
		t.Fatalf("no digest: %v", err)
	}
}

// TestPrepareUKIPerHost: per-host signing makes two keys and hands them to
// the target tools as /proc fd paths; a failure leaves the ESP unchanged
// and removes the work dir.
func TestPrepareUKIPerHost(t *testing.T) {
	n := countKeyGeneration(t, false)
	uki := testPE(t, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".osrel", "ID=x"})
	o, cmd, root, esp := ukiTestHost(t, config.SecureBootSigningPerHost, uki)
	cmd.setResult("chroot "+root, []byte("sbsign: failed"), errors.New("exit 1"))
	_, err := o.prepareUKI(context.Background(), root, esp)
	if err == nil || !strings.Contains(err.Error(), "sign the UKI") {
		t.Fatalf("err = %v", err)
	}
	if *n != 2 {
		t.Fatalf("%d keys generated, want the PCR policy key and the Secure Boot key", *n)
	}
	script := strings.Join(cmd.calls[0].args, " ")
	for _, want := range []string{"systemd-measure sign", "--linux=", "--cmdline=", "--osrel=", "--private-key=/proc/",
		"ukify build --join-pcrsig=", "sbsign --key /proc/"} {
		if !strings.Contains(script, want) {
			t.Errorf("signing command lacks %q: %s", want, script)
		}
	}
	if strings.Contains(script, "PRIVATE KEY") {
		t.Fatal("a private key is in the command line")
	}
	if _, err := os.Stat(ukiWorkDir); !os.IsNotExist(err) {
		t.Fatalf("work dir left: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi")); !bytes.Equal(got, uki) {
		t.Fatal("a failed signing changed the ESP")
	}

	// An already signed UKI is refused before any key is made.
	*n = 0
	o, _, root, esp = ukiTestHost(t, config.SecureBootSigningPerHost, testPE(t, true, [2]string{".linux", "k"}))
	if _, err := o.prepareUKI(context.Background(), root, esp); err == nil || *n != 0 {
		t.Fatalf("signed UKI: err=%v keys=%d", err, *n)
	}
}

func TestPrepareUKINeedsOneUKI(t *testing.T) {
	o, _, root, esp := ukiTestHost(t, config.SecureBootSigningCI, testPE(t, true, [2]string{".linux", "k"}))
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/other.efi"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(context.Background(), root, esp); err == nil || !strings.Contains(err.Error(), "exactly one UKI") {
		t.Fatalf("err = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "usr/bin/systemd-cryptenroll")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(context.Background(), root, esp); err == nil || !strings.Contains(err.Error(), "systemd-cryptenroll") {
		t.Fatalf("missing tool: %v", err)
	}
}

func TestPrivateKeyMemfdWiped(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	if err != nil {
		t.Fatal(err)
	}
	f, err := privateKeyMemfd(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(procFD(f))
	if err != nil || !bytes.Contains(got, []byte("PRIVATE KEY")) {
		t.Fatalf("memfd via /proc: %v", err)
	}
	wipeMemfd(f)
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
	// The empty passphrase is wiped only after the checked enrollment.
	last := -1
	for _, s := range []string{"--tpm2-public-key-pcrs=11", "--tpm2-signature=", "--tpm2-pcrs=", "luksKillSlot", "systemctl disable"} {
		i := strings.Index(ukiEnrollScript, s)
		if i <= last {
			t.Fatalf("%q missing or out of order", s)
		}
		last = i
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
		var n []string
		for _, s := range newTestOrchestrator(t, cfg, &mockProvider{}).provisionSteps() {
			n = append(n, s.Name)
		}
		return n
	}
	uki := strings.Join(names(config.DiskEncryptionLUKS2TPM2UKI), " ")
	if !strings.Contains(uki, "create-mirror-efi-boot-entry create-uki-boot-entry teardown-chroot") {
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
	o.encryption = &encryptionState{uki: &ukiState{loader: `\EFI\Linux\u.efi`}}
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
}
