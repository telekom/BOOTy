//go:build e2e

package kvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/secureboot"
)

const sysextFiles = "var/lib/extensions/booty-modules"

// TestModuleSigningSysextVM provisions an Ubuntu cloud image with
// MODULE_SIGNING_TARGET=sysext (experimental, docs/module-signing.md,
// dm-verity roots). It checks that
//   - /usr of the image, which a dm-verity root would protect, stays
//     byte-identical: the module there stays unsigned;
//   - with Secure Boot on and the per-host key enrolled as MOK,
//     systemd-sysext merges the extension (signed dm-verity root hash) and
//     the signed copy of the module loads;
//   - an extension whose root hash signature comes from another host's key
//     is not merged, and the unsigned module of the image is refused;
//   - an extension with a changed data block fails the read (dm-verity), so
//     the module does not load.
func TestModuleSigningSysextVM(t *testing.T) {
	dir := t.TempDir()
	var usrBefore string
	gzImage, mods := prepareModuleImage(t, requireModSignHost(t), dir, func(mnt, kernel string) {
		// The image lists its out-of-tree module in modules.dep, as an image
		// build that ran depmod does; BOOTy does not run depmod in sysext mode.
		run(t, "depmod in the image", "depmod", "-b", mnt, kernel)
		usrBefore = treeDigest(t, filepath.Join(mnt, "usr"))
	})
	signedMod := mods[0]
	m := newModSignVM(t, dir, gzImage, map[string]string{
		"HOSTNAME": "modsysext-e2e", "SECUREBOOT_SIGNING": "per-host", "MODULE_SIGNING_TARGET": "sysext",
	})

	m.provision("provision")
	var goodSig, raw []byte
	withDiskPartition(t, m.disk, dir, "ext4", func(mnt string) {
		if got := treeDigest(t, filepath.Join(mnt, "usr")); got != usrBefore {
			t.Fatalf("/usr of the image changed: %s, was %s", got, usrBefore)
		}
		for _, ext := range []string{".raw", ".verity", ".roothash", ".roothash.p7s"} {
			if info, err := os.Stat(filepath.Join(mnt, sysextFiles+ext)); err != nil || info.Size() == 0 {
				t.Fatalf("sysext file %s: %v", ext, err)
			}
		}
		goodSig = readFile(t, filepath.Join(mnt, sysextFiles+".roothash.p7s"))
		raw = readFile(t, filepath.Join(mnt, sysextFiles+".raw"))
	})
	t.Log("/usr of the image is unchanged; the sysext and its signed root hash are in /var/lib/extensions")

	m.enroll("mok-enroll")
	setSecureBoot(t, m.vars, true)

	report := m.check("secure boot on")
	requireLine(t, report, "SECUREBOOT=1")
	if os.Getenv("BOOTY_SYSEXT_ROOTHASH_SIG") == "unsupported" {
		// The kernel refuses the root hash signature (Ubuntu 24.04, 6.8):
		// the extension stays out, the module stays unsigned and refused,
		// and the boot goes on.
		requireLine(t, report, "SYSEXT_MERGED=0")
		requireLine(t, report, "SIGNED_MODULE=refused")
		if !strings.Contains(report, "Root hash verification failed") {
			t.Fatalf("no root hash verification error:\n%s", report)
		}
		return
	}
	requireLine(t, report, "SYSEXT_MERGED=1")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "SIGNED_SIGNER=BOOTy module signing modsysext-e2e")
	requireLine(t, report, "UNSIGNED_MODULE=refused")

	// Root hash signed by another host's key: the kernel rejects the
	// signature, the extension does not merge, and the unsigned module of
	// the image is refused.
	withDiskPartition(t, m.disk, dir, "ext4", func(mnt string) {
		rootHash := bytes.TrimSpace(readFile(t, filepath.Join(mnt, sysextFiles+".roothash")))
		writeFile(t, filepath.Join(mnt, sysextFiles+".roothash.p7s"), string(signWithOtherHost(t, rootHash)))
	})
	report = m.check("root hash signed by another key")
	requireLine(t, report, "SYSEXT_MERGED=0")
	requireLine(t, report, "SIGNED_MODULE=refused")

	// One changed byte in the module data: dm-verity fails the read.
	withDiskPartition(t, m.disk, dir, "ext4", func(mnt string) {
		writeFile(t, filepath.Join(mnt, sysextFiles+".roothash.p7s"), string(goodSig))
		off := bytes.Index(raw, signedMod.raw[:512])
		if off < 0 {
			t.Fatal("module data not found in the sysext image")
		}
		tampered := bytes.Clone(raw)
		tampered[off+200] ^= 0xff
		writeFile(t, filepath.Join(mnt, sysextFiles+".raw"), string(tampered))
	})
	report = m.check("changed sysext data")
	requireLine(t, report, "SIGNED_MODULE=refused")
	if !strings.Contains(report, "verity") {
		t.Fatalf("no dm-verity error for the changed block:\n%s", report)
	}
}

// treeDigest returns one SHA-256 over the paths, modes, link targets and
// contents below dir.
func treeDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s %v\n", rel, info.Mode())
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintln(h, target)
		case d.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			fmt.Fprintln(h, hex.EncodeToString(sum[:]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// signWithOtherHost signs data with the per-host key of another host.
func signWithOtherHost(t *testing.T, data []byte) []byte {
	t.Helper()
	root := t.TempDir()
	writeImageFile(t, root, "lib/modules/0/updates/m.ko", []byte("\x7fELF"))
	res, err := secureboot.SignModulesInto(root, t.TempDir(), "BOOTy module signing other-host",
		func([]byte) ([]byte, error) { return data, nil })
	if err != nil {
		t.Fatal(err)
	}
	return res.SealSignature
}
