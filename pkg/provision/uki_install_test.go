//go:build linux

package provision

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

// perHostUKI is an unsigned UKI with .osrel after .cmdline, so the
// per-host .cmdline moves the data of a later section.
func perHostUKI(t *testing.T) []byte {
	t.Helper()
	return testPE(t, false, [2]string{".linux", "k"}, [2]string{".cmdline", "root=x"}, [2]string{".osrel", "ID=x"})
}

// TestUKIPerHostCmdline: per-host signing appends the kernel parameters of
// the GRUB drop-in to the .cmdline, and the signed PCR 11 policy is the
// one of the UKI with that .cmdline.
func TestUKIPerHostCmdline(t *testing.T) {
	countKeyGeneration(t, false)
	o, _, root, esp := ukiTestHost(t, config.SigningPerHost, perHostUKI(t))
	o.cfg.Provision.SerialConsole = "none"
	o.cfg.Provision.ExtraKernelParams = "booty.test=1"
	if _, err := o.prepareUKI(root, esp); err != nil {
		t.Fatal(err)
	}
	signed, err := os.ReadFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi"))
	if err != nil {
		t.Fatal(err)
	}
	secs, ok, err := ukiSections(signed)
	if err != nil || !ok {
		t.Fatalf("signed=%v err=%v", ok, err)
	}
	if string(secs["cmdline"]) != "root=x booty.test=1" || string(secs["osrel"]) != "ID=x" || string(secs["linux"]) != "k" {
		t.Fatalf("sections %q", secs)
	}
	grub, err := o.config.KernelParams(o.cfg)
	if err != nil || grub != "booty.test=1" {
		t.Fatalf("GRUB parameters %q, %v", grub, err)
	}
	var doc struct{ SHA256 []ukiPCRSigEntry }
	if err := json.Unmarshal(secs["pcrsig"], &doc); err != nil {
		t.Fatal(err)
	}
	measured := map[string][]byte{"linux": secs["linux"], "osrel": secs["osrel"], "cmdline": secs["cmdline"], "pcrpkey": secs["pcrpkey"]}
	for i, pol := range ukiPCRPolicies(measured) {
		if i >= len(doc.SHA256) || doc.SHA256[i].Pol != hex.EncodeToString(pol[:]) {
			t.Fatalf("phase %d: .pcrsig %+v does not sign the policy of the per-host .cmdline", i, doc.SHA256)
		}
	}

	// With UKI_KEEP_IMAGE_CMDLINE the image .cmdline stays.
	o, _, root, esp = ukiTestHost(t, config.SigningPerHost, perHostUKI(t))
	o.cfg.Provision.SerialConsole = "none"
	o.cfg.Provision.ExtraKernelParams = "booty.test=1"
	o.cfg.Provision.DiskEncryption.UKIKeepImageCmdline = true
	if _, err := o.prepareUKI(root, esp); err != nil {
		t.Fatal(err)
	}
	signed, _ = os.ReadFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi"))
	if secs, _, _ := ukiSections(signed); string(secs["cmdline"]) != "root=x" {
		t.Fatalf("UKI_KEEP_IMAGE_CMDLINE: .cmdline %q", secs["cmdline"])
	}
}

// TestUKIPCRPolicyPerHostCmdline: the PCR 11 policies of a UKI with a
// merged .cmdline are the ones of `systemd-measure sign --linux=... --osrel=...
// --cmdline=... --pcrpkey=... --bank=sha256` (systemd 259) for the same contents.
func TestUKIPCRPolicyPerHostCmdline(t *testing.T) {
	pols := ukiPCRPolicies(map[string][]byte{"linux": []byte("k"), "osrel": []byte("ID=x"), "cmdline": []byte("root=x booty.test=1"), "pcrpkey": []byte("pub")})
	want := []string{"8f854701f7438af7a8045afd78f2a1066d32a37b8da2d1152b7992fd00fa4e11", "a78393b10f28732ebb6dcd64a53939702e298fb860037c0ce84550a547bde2f4",
		"7ad0908307b9d3a289f6951bf6031dab4b72602b38e77b7fed46eba444e8e71b", "b82f35b6a37d0596835677c85fc2efd1aa6f552ca660fbbad8340d3edd3bd477"}
	for i, p := range pols {
		if hex.EncodeToString(p[:]) != want[i] {
			t.Errorf("phase %d: %x, systemd-measure %s", i, p, want[i])
		}
	}
}

// TestUKIPerHostCmdlineRefused: denied, non-ASCII and too long parameters
// fail before any key is made.
func TestUKIPerHostCmdlineRefused(t *testing.T) {
	n := countKeyGeneration(t, false)
	for _, params := range []string{"init=/bin/sh", "rd.break=pre-mount", "systemd.debug-shell=1", "rd.systemd.debug-shell", "single",
		"systemd.unit=rescue.target", "rd.systemd.break=pre-switch-root", "systemd.break=pre-basic", "systemd.wants=debug-shell.service",
		"rd.systemd.wants=debug-shell.service", "booty.x=é", strings.Repeat("a", ukiMaxCmdline)} {
		o, _, root, esp := ukiTestHost(t, config.SigningPerHost, perHostUKI(t))
		o.cfg.Provision.SerialConsole = "none"
		o.cfg.Provision.ExtraKernelParams = params
		if _, err := o.prepareUKI(root, esp); err == nil || *n != 0 {
			t.Errorf("%.20q: err=%v keys=%d", params, err, *n)
		}
	}
	if err := checkUKIKernelParams("hugepages=16 isolcpus=2-3 console=ttyS0,115200n8 ds=nocloud"); err != nil {
		t.Errorf("ordinary parameters refused: %v", err)
	}
}

func TestReplacePESection(t *testing.T) {
	uki := perHostUKI(t)
	out, err := replacePESection(uki, ".cmdline", []byte("root=x "+strings.Repeat("a", 0x100)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseUnsignedPE(out); err != nil {
		t.Fatalf("the rewritten PE: %v", err)
	}
	secs, _, _ := ukiSections(out)
	if len(secs["cmdline"]) != 0x107 || string(secs["osrel"]) != "ID=x" {
		t.Fatalf("sections %q", secs)
	}
	// testPE aligns sections to 0x200: a longer .cmdline needs the address
	// of .osrel.
	if _, err := replacePESection(uki, ".cmdline", bytes.Repeat([]byte("a"), 0x201)); err == nil {
		t.Error("a .cmdline overlapping the next section accepted")
	}
	if _, err := replacePESection(uki, ".dtb", []byte("x")); err == nil {
		t.Error("a missing section accepted")
	}
	// A data directory (the debug directory, 6) into .osrel, which moves.
	b := slices.Clone(uki)
	osrelVA := binary.LittleEndian.Uint32(b[0x148+2*40+12:])
	binary.LittleEndian.PutUint32(b[0x58+112+8*6:], osrelVA)
	binary.LittleEndian.PutUint32(b[0x58+112+8*6+4:], 4)
	if _, err := replacePESection(b, ".cmdline", []byte("root=y")); err == nil {
		t.Error("a data directory into a moving section accepted")
	}
	if _, err := replacePESection(testPE(t, true, [2]string{".cmdline", "x"}), ".cmdline", []byte("y")); err == nil {
		t.Error("a signed PE accepted")
	}
	// SizeOfHeaders inside the section table, the section data from there.
	b = testPE(t, false, [2]string{".cmdline", "root=x"})
	binary.LittleEndian.PutUint32(b[0x58+60:], 0x150)
	binary.LittleEndian.PutUint32(b[0x148+20:], 0x150)
	b = slices.Delete(b, 0x150, 0x200)
	if _, err := replacePESection(b, ".cmdline", []byte("root=y")); err == nil || !strings.Contains(err.Error(), "SizeOfHeaders") {
		t.Errorf("section data over the headers: %v", err)
	}
}

// TestInstallUKIFileAtomic: the UKI is replaced through a temporary file;
// a failed write leaves the original intact and no temporary file.
func TestInstallUKIFileAtomic(t *testing.T) {
	esp := t.TempDir()
	rel := "EFI/Linux/u.efi"
	if err := os.MkdirAll(filepath.Join(esp, "EFI/Linux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(esp, rel), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(esp)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close() //nolint:errcheck // test
	old := syncUKIFile
	syncUKIFile = func(*os.File) error { return errors.New("power loss") }
	err = installUKIFile(root, rel, []byte("new"), false)
	syncUKIFile = old
	if err == nil {
		t.Fatal("failed write reported success")
	}
	if b, _ := os.ReadFile(filepath.Join(esp, rel)); string(b) != "old" {
		t.Fatalf("original %q after a failed write", b)
	}
	if _, err := os.Stat(filepath.Join(esp, "EFI/Linux/.u.efi.booty-tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file left: %v", err)
	}
	// A leftover temporary file does not stop the install.
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/.u.efi.booty-tmp"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installUKIFile(root, rel, []byte("new"), false); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(esp, "EFI/Linux"))
	if b, _ := os.ReadFile(filepath.Join(esp, rel)); string(b) != "new" || len(entries) != 1 {
		t.Fatalf("UKI %q, entries %v", b, entries)
	}
}

// TestPrepareUKIESPRoom: without room for a second copy the per-host
// install fails before any key is made, unless UKI_INPLACE_WRITE is set.
func TestPrepareUKIESPRoom(t *testing.T) {
	n := countKeyGeneration(t, false)
	old := espFreeBytes
	espFreeBytes = func(string) (uint64, error) { return 1 << 20, nil }
	t.Cleanup(func() { espFreeBytes = old })
	o, _, root, esp := ukiTestHost(t, config.SigningPerHost, perHostUKI(t))
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "UKI_INPLACE_WRITE") || *n != 0 {
		t.Fatalf("err=%v keys=%d", err, *n)
	}
	o.cfg.Provision.DiskEncryption.UKIInplaceWrite = true
	if _, err := o.prepareUKI(root, esp); err != nil {
		t.Fatalf("UKI_INPLACE_WRITE: %v", err)
	}
}

// TestPrepareUKILeftoverAndTruncated: a temporary file of an interrupted
// install is removed; a truncated UKI is reported as such.
func TestPrepareUKILeftoverAndTruncated(t *testing.T) {
	countKeyGeneration(t, false)
	o, _, root, esp := ukiTestHost(t, config.SigningPerHost, perHostUKI(t))
	tmp := filepath.Join(esp, "EFI/Linux/.ubuntu-6.8.efi.booty-tmp")
	if err := os.WriteFile(tmp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(root, esp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover temporary file: %v", err)
	}
	uki := perHostUKI(t)
	o, _, root, esp = ukiTestHost(t, config.SigningPerHost, uki)
	if err := os.WriteFile(filepath.Join(esp, "EFI/Linux/ubuntu-6.8.efi"), uki[:0x100], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.prepareUKI(root, esp); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("err = %v, want truncated", err)
	}
}
