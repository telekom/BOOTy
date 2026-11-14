//go:build e2e

package kvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	osImage := os.Getenv("MIRROR_OS_IMAGE")
	helper := os.Getenv("BOOTY_MOK_ENROLL_EFI")
	code := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd")
	varsTemplate := envOrDefault("OVMF_VARS_MS", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd")
	for _, f := range []string{osImage, helper, code, varsTemplate} {
		if _, err := os.Stat(f); f == "" || err != nil {
			failOrSkipUnsupportedHost(t, "module sysext asset missing (MIRROR_OS_IMAGE, BOOTY_MOK_ENROLL_EFI, OVMF): %q %v", f, err)
		}
	}
	for _, tool := range []string{"virt-fw-vars", "swtpm", "zstd", "depmod"} {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}

	dir := t.TempDir()
	var usrBefore string
	gzImage, mods := prepareModuleImage(t, osImage, dir, func(mnt, kernel string) {
		// The image lists its out-of-tree module in modules.dep, as an image
		// build that ran depmod does; BOOTy does not run depmod in sysext mode.
		run(t, "depmod in the image", "depmod", "-b", mnt, kernel)
		usrBefore = treeDigest(t, filepath.Join(mnt, "usr"))
	})
	signedMod := mods[0]
	helperEFI, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	initramfs := buildProvisionInitramfsWithFiles(t, map[string]string{
		"HOSTNAME":                  "modsysext-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     startImageServer(t, gzImage) + "/image.gz",
		"MODE":                      "provision",
		"MIN_DISK_SIZE_GB":          "1",
		"MODULE_SIGNING_ENABLED":    "true",
		"MODULE_SIGNING_TARGET":     "sysext",
	}, map[string]string{"usr/lib/booty/mok-enroll.efi": string(helperEFI)})
	kernel := findKernel(t)

	vars := filepath.Join(dir, "OVMF_VARS.fd")
	copyBinary(t, varsTemplate, vars)
	setSecureBoot(t, vars, false)
	// Raw, so the test can mount the provisioned root between boots.
	disk := filepath.Join(dir, "disk.raw")
	run(t, "create disk", "qemu-img", "create", "-f", "raw", disk, "6G")
	boots := 0
	vm := func(extra ...string) []string {
		boots++
		tpm := startSWTPM(t, filepath.Join(dir, fmt.Sprintf("tpm%d", boots)))
		return append([]string{
			"-machine", "q35,smm=on,accel=kvm:tcg", "-cpu", mirrorQEMUCPUModel(), "-m", "2048", "-smp", "2",
			"-nographic", "-no-reboot",
			"-global", "driver=cfi.pflash01,property=secure,value=on",
			"-drive", "if=pflash,format=raw,unit=0,readonly=on,file=" + code,
			"-drive", "if=pflash,format=raw,unit=1,file=" + vars,
			"-drive", "file=" + disk + ",format=raw,if=virtio",
			"-chardev", "socket,id=chrtpm,path=" + tpm,
			"-tpmdev", "emulator,id=tpm0,chardev=chrtpm", "-device", "tpm-tis,tpmdev=tpm0",
		}, extra...)
	}
	check := func(phase string) string {
		return modCheckReport(t, phase, runMirrorQEMU(t, phase, vm("-net", "none"), 8*time.Minute))
	}

	out := runMirrorQEMU(t, "provision", vm("-kernel", kernel, "-initrd", initramfs,
		"-append", runnerKernelCmdline+" net.ifnames=0", "-net", "nic,model=virtio", "-net", "user"), 10*time.Minute)
	if !strings.Contains(string(out), "step=report-success") || strings.Contains(string(out), "provisioning step failed") {
		t.Fatal("provisioning did not succeed")
	}
	var goodSig, raw []byte
	withDiskRoot(t, disk, dir, func(mnt string) {
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

	out = runMirrorQEMU(t, "mok-enroll", vm("-net", "none"), 3*time.Minute)
	if !strings.Contains(string(out), "BOOTy mok-enroll: MokList updated") {
		t.Fatalf("mok-enroll.efi did not run:\n%s", tail(out, 4000))
	}
	setSecureBoot(t, vars, true)

	report := check("secure boot on")
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
	withDiskRoot(t, disk, dir, func(mnt string) {
		rootHash := bytes.TrimSpace(readFile(t, filepath.Join(mnt, sysextFiles+".roothash")))
		writeFile(t, filepath.Join(mnt, sysextFiles+".roothash.p7s"), string(signWithOtherHost(t, rootHash)))
	})
	report = check("root hash signed by another key")
	requireLine(t, report, "SYSEXT_MERGED=0")
	requireLine(t, report, "SIGNED_MODULE=refused")

	// One changed byte in the module data: dm-verity fails the read.
	withDiskRoot(t, disk, dir, func(mnt string) {
		writeFile(t, filepath.Join(mnt, sysextFiles+".roothash.p7s"), string(goodSig))
		off := bytes.Index(raw, signedMod.raw[:512])
		if off < 0 {
			t.Fatal("module data not found in the sysext image")
		}
		tampered := bytes.Clone(raw)
		tampered[off+200] ^= 0xff
		writeFile(t, filepath.Join(mnt, sysextFiles+".raw"), string(tampered))
	})
	report = check("changed sysext data")
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

// withDiskRoot mounts the root partition (1) of the raw disk while f runs.
func withDiskRoot(t *testing.T, disk, dir string, f func(mnt string)) {
	t.Helper()
	loop := strings.TrimSpace(string(runOutput(t, "attach disk", "losetup", "-P", "--show", "-f", disk)))
	mnt := filepath.Join(dir, "diskroot")
	defer func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() }()
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "mount provisioned root", "mount", loop+"p1", mnt)
	defer func() { _ = exec.CommandContext(context.Background(), "umount", mnt).Run() }()
	f(mnt)
	run(t, "unmount provisioned root", "umount", mnt)
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
