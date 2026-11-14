//go:build e2e

package kvm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/telekom/BOOTy/pkg/provision"
)

// Scenarios disk-encryption-uki-per-host and disk-encryption-uki-ci
// (.github/workflows/kvm-matrix.yml): DISK_ENCRYPTION=luks2-tpm2-uki with
// SECUREBOOT_SIGNING=per-host and =ci. The test acts as the image builder:
// it adds a UKI to the Ubuntu cloud image whose initramfs unlocks the root
// by PARTUUID with the TPM2 or the empty passphrase, pushes the image to an
// OCI registry with the UKI digest annotation, and signs it with cosign.

// ukiCheckScript reports the volume and boot state on every boot.
const ukiCheckScript = `#!/bin/sh
exec >/dev/ttyS0 2>&1
. /etc/booty/luks-tpm2.env
dev=/dev/disk/by-uuid/$LUKS_UUID
echo "` + luksCheckBegin + `"
echo "SECUREBOOT=$(od -An -tu1 -j4 -N1 /sys/firmware/efi/efivars/SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c | tr -d ' ')"
echo "STUB=$(ls /sys/firmware/efi/efivars/StubInfo-* >/dev/null 2>&1 && echo 1 || echo 0)"
echo "CMDLINE=$(tr ' ' ',' </proc/cmdline)"
echo "PCR7=$(cat /sys/class/tpm/tpm0/pcr-sha256/7 2>/dev/null)"
echo "PCR11=$(cat /sys/class/tpm/tpm0/pcr-sha256/11 2>/dev/null)"
echo "ROOT_TYPE=$(lsblk -no TYPE "$(findmnt -no SOURCE /)")"
echo "KEYSLOTS=$(cryptsetup luksDump "$dev" | sed -n '/^Keyslots:/,/^Tokens:/p' | sed -n 's/^  \([0-9]*\): luks2.*/\1/p' | tr '\n' ',')"
echo "TPM2_TOKENS=$(cryptsetup luksDump "$dev" | grep -c 'systemd-tpm2')"
echo "TPM2_PUBKEY_PCRS=$(cryptsetup luksDump "$dev" | sed -n 's/^\s*tpm2-pubkey-pcrs:\s*//p' | tr -d ' ')"
echo "TPM2_HASH_PCRS=$(cryptsetup luksDump "$dev" | sed -n 's/^\s*tpm2-hash-pcrs:\s*//p' | tr -d ' ')"
echo "TOKEN_SUM=$(cryptsetup luksDump "$dev" | sed -n '/^Tokens:/,/^Digests:/p' | sha256sum | cut -c1-16)"
echo "EMPTY_PASS=$(cryptsetup open --test-passphrase --key-file /dev/null "$dev" >/dev/null 2>&1 && echo opens || echo refused)"
echo "UNIT=$(systemctl is-enabled booty-luks-tpm2-enroll.service 2>&1)"
journalctl -b -o cat -u booty-luks-tpm2-enroll.service --no-pager | tail -n 30 | sed 's/^/ENROLL /'
journalctl -b -o cat --no-pager | grep -i -e 'tpm2' -e 'cryptsetup' | tail -n 30 | sed 's/^/JOURNAL /'
echo "` + luksCheckEnd + `"
systemctl poweroff --no-block
`

// ukiKeys is a PCR policy key and a Secure Boot key with certificate, as
// PEM files.
type ukiKeys struct{ pcrKey, pcrPub, sbKey, sbCert string }

func newUKIKeys(t *testing.T, dir, prefix string) ukiKeys {
	t.Helper()
	write := func(name string, b *pem.Block) string {
		p := filepath.Join(dir, prefix+name)
		if err := os.WriteFile(p, pem.EncodeToMemory(b), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	key := func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	pcr, sb := key(), key()
	pcrDER, _ := x509.MarshalPKCS8PrivateKey(pcr)
	pubDER, _ := x509.MarshalPKIXPublicKey(&pcr.PublicKey)
	sbDER, _ := x509.MarshalPKCS8PrivateKey(sb)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "BOOTy e2e " + prefix},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}
	cert, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &sb.PublicKey, sb)
	if err != nil {
		t.Fatal(err)
	}
	return ukiKeys{
		pcrKey: write("pcr.key", &pem.Block{Type: "PRIVATE KEY", Bytes: pcrDER}),
		pcrPub: write("pcr.pub", &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}),
		sbKey:  write("sb.key", &pem.Block{Type: "PRIVATE KEY", Bytes: sbDER}),
		sbCert: write("sb.crt", &pem.Block{Type: "CERTIFICATE", Bytes: cert}),
	}
}

// ukiImage is the prepared OS image.
type ukiImage struct {
	gz       string
	ukiSHA   string
	ukiName  string
	espPart  int
	variants map[string]string // extra UKIs built with the image, on the host
}

// ukiVariant is an extra UKI that the builder makes: a cmdline marker and
// the keys that sign it.
type ukiVariant struct {
	marker string
	pcr    ukiKeys // PCR policy key
	sb     ukiKeys // Secure Boot key
}

// prepareUKIImage installs the UKI tools, builds the initramfs that unlocks
// the root by PARTUUID, and puts a UKI on the ESP: unsigned with ci == nil,
// else signed with the keys of ci. variants are built too and copied out.
func prepareUKIImage(t *testing.T, osImage, dir string, debs []string, ci *ukiKeys, variants map[string]ukiVariant) ukiImage {
	t.Helper()
	raw := filepath.Join(dir, "os.raw")
	run(t, "convert OS image to raw", "qemu-img", "convert", "-O", "raw", osImage, raw)
	loop := strings.TrimSpace(string(runOutput(t, "attach image", "losetup", "-P", "--show", "-f", raw)))
	defer func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() }()
	label := func(part string) string {
		out, _ := exec.CommandContext(context.Background(), "blkid", "-o", "value", "-s", "LABEL", part).Output()
		return strings.TrimSpace(string(out))
	}
	img := ukiImage{variants: map[string]string{}}
	var bootPart, espPart string
	for i := 2; i <= 16; i++ {
		p := fmt.Sprintf("%sp%d", loop, i)
		switch label(p) {
		case "BOOT":
			bootPart = p
		case "UEFI":
			espPart, img.espPart = p, i
		}
	}
	if bootPart == "" || espPart == "" {
		t.Fatalf("no BOOT or UEFI partition on %s", loop)
	}
	rootPartUUID := strings.TrimSpace(string(runOutput(t, "root PARTUUID", "blkid", "-o", "value", "-s", "PARTUUID", loop+"p1")))
	rootFSUUID := strings.TrimSpace(string(runOutput(t, "root UUID", "blkid", "-o", "value", "-s", "UUID", loop+"p1")))

	mnt := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	var mounts []string
	mount := func(args ...string) {
		run(t, "mount "+args[len(args)-1], "mount", args...)
		mounts = append(mounts, args[len(args)-1])
	}
	unmountAll := func() {
		for i := len(mounts) - 1; i >= 0; i-- {
			_ = exec.CommandContext(context.Background(), "umount", mounts[i]).Run()
		}
		mounts = nil
	}
	defer unmountAll()
	mount(loop+"p1", mnt)
	mount(bootPart, filepath.Join(mnt, "boot"))
	mount(espPart, filepath.Join(mnt, "boot/efi"))
	for _, fs := range []string{"proc", "sys", "dev"} {
		mount("--bind", "/"+fs, filepath.Join(mnt, fs))
	}
	mount("-t", "tmpfs", "tmpfs", filepath.Join(mnt, "run"))
	resolv, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	luksWriteImageFile(t, mnt, "run/systemd/resolve/stub-resolv.conf", resolv, 0o644)
	chroot := func(desc, script string) {
		run(t, desc, "chroot", mnt, "/bin/sh", "-ec", script)
	}
	chroot("install the UKI tools", "export DEBIAN_FRONTEND=noninteractive; apt-get update -q; "+
		"apt-get install -y -q --no-install-recommends systemd-ukify systemd-boot-efi sbsigntool python3-pefile; apt-get clean; df -h / /boot/efi")
	for _, deb := range debs {
		luksWriteImageFile(t, mnt, "tmp/"+filepath.Base(deb), readFile(t, deb), 0o644)
		chroot("install "+filepath.Base(deb), "dpkg -i /tmp/"+filepath.Base(deb)+"; rm /tmp/"+filepath.Base(deb))
	}

	// The root file system of the cloud image is nearly full: the builder
	// works in the tmpfs on /run.
	// The builder's crypttab: generic, by the PARTUUID of the root, which
	// BOOTy keeps. try-empty-password opens the volume until the first
	// Secure Boot boot binds it to the TPM2.
	luksWriteImageFile(t, mnt, "etc/crypttab", []byte(fmt.Sprintf(
		"booty_crypt PARTUUID=%s none luks,x-initrd.attach,tpm2-device=auto,try-empty-password=yes\n", rootPartUUID)), 0o644)
	kvers, err := os.ReadDir(filepath.Join(mnt, "lib/modules"))
	if err != nil || len(kvers) != 1 {
		t.Fatalf("want one kernel in the image: %v %v", kvers, err)
	}
	kver := kvers[0].Name()
	chroot("build the UKI initramfs", `libs=""
for l in libtss2-esys.so.0 libtss2-rc.so.0 libtss2-mu.so.0 libtss2-tcti-device.so.0 cryptsetup/libcryptsetup-token-systemd-tpm2.so; do
	libs="$libs $(ls /usr/lib/*-linux-gnu/$l)"
done
dracut --force --no-hostonly --tmpdir /run --add "crypt systemd-cryptsetup systemd-pcrextend" --omit "tpm2-tss plymouth" \
	--install "/etc/crypttab $libs" /run/uki-initrd.img `+kver)
	luksWriteImageFile(t, mnt, "usr/local/lib/booty-luks-check.sh", []byte(ukiCheckScript), 0o755)
	luksWriteImageFile(t, mnt, "etc/systemd/system/booty-luks-check.service", []byte(luksCheckUnit), 0o644)
	if err := os.Symlink("/etc/systemd/system/booty-luks-check.service",
		filepath.Join(mnt, "etc/systemd/system/multi-user.target.wants/booty-luks-check.service")); err != nil {
		t.Fatal(err)
	}

	keyDir := filepath.Join(mnt, "tmp/uki-keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	inImage := func(k ukiKeys) ukiKeys {
		for _, p := range []string{k.pcrKey, k.pcrPub, k.sbKey, k.sbCert} {
			if err := os.WriteFile(filepath.Join(keyDir, filepath.Base(p)), readFile(t, p), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		c := func(p string) string { return "/tmp/uki-keys/" + filepath.Base(p) }
		return ukiKeys{c(k.pcrKey), c(k.pcrPub), c(k.sbKey), c(k.sbCert)}
	}
	build := func(out, marker string, pcr, sb *ukiKeys) {
		args := []string{"ukify", "build", "--linux=/boot/vmlinuz-" + kver, "--initrd=/run/uki-initrd.img", "--uname=" + kver,
			"--os-release=@/etc/os-release", "--output=" + out,
			"--cmdline='root=UUID=" + rootFSUUID + " ro console=tty1 console=ttyS0 " + marker + "'"}
		if pcr != nil {
			p := inImage(*pcr)
			args = append(args, "--pcr-private-key="+p.pcrKey, "--pcr-public-key="+p.pcrPub, "--pcr-banks=sha256")
		}
		if sb != nil {
			s := inImage(*sb)
			args = append(args, "--secureboot-private-key="+s.sbKey, "--secureboot-certificate="+s.sbCert)
		}
		chroot("build "+out, "mkdir -p \"$(dirname "+out+")\"; "+strings.Join(args, " "))
	}
	img.ukiName = "ubuntu-" + kver + ".efi"
	build("/boot/efi/EFI/Linux/"+img.ukiName, "booty.uki=v1", ci, ci)
	for name, v := range variants {
		build("/run/"+name+".efi", "booty.uki="+v.marker, &v.pcr, &v.sb)
		dst := filepath.Join(dir, name+".efi")
		copyBinary(t, filepath.Join(mnt, "run", name+".efi"), dst)
		img.variants[name] = dst
	}
	uki := readFile(t, filepath.Join(mnt, "boot/efi/EFI/Linux", img.ukiName))
	sum := sha256.Sum256(uki)
	img.ukiSHA = hex.EncodeToString(sum[:])
	img.variants["v1"] = filepath.Join(dir, "v1.efi")
	if err := os.WriteFile(img.variants["v1"], uki, 0o644); err != nil {
		t.Fatal(err)
	}
	chroot("remove the builder files", "rm -rf /tmp/uki-keys; apt-get clean")
	unmountAll()
	run(t, "detach image", "losetup", "-d", loop)
	img.gz = compressGzip(t, raw)
	if err := os.Remove(raw); err != nil {
		t.Fatal(err)
	}
	return img
}

// pushUKIImage pushes the image with the UKI digest annotation, signs it
// with cosign and returns the oci:// reference for the guest and the PEM
// cosign public key.
func pushUKIImage(t *testing.T, img ukiImage) (ref, keyPEM string) {
	t.Helper()
	key := newCosignTestKey(t)
	pushHost, guestHost := startABOCIRegistry(t)
	base, err := mutate.AppendLayers(empty.Image, static.NewLayer(readFile(t, img.gz), types.OCILayer))
	if err != nil {
		t.Fatal(err)
	}
	annotated, ok := mutate.Annotations(base, map[string]string{provision.UKIDigestAnnotation: img.ukiSHA}).(v1.Image)
	if !ok {
		t.Fatal("annotated image is not an image")
	}
	tag, err := name.NewTag(pushHost + "/test/uki-os:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, annotated); err != nil {
		t.Fatalf("push %s: %v", tag, err)
	}
	digest, err := annotated.Digest()
	if err != nil {
		t.Fatal(err)
	}
	signCosignTestImage(t, pushHost, "test/uki-os", digest.String(), key)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return "oci://" + guestHost + "/test/uki-os@" + digest.String(), string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// ukiVM is one machine: its disk, firmware variables and TPM state.
type ukiVM struct {
	t                     *testing.T
	dir, code, vars, disk string
	boots                 int
	logs                  [][]byte
}

func newUKIVM(t *testing.T, dir string) *ukiVM {
	t.Helper()
	code := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd")
	varsTemplate := envOrDefault("OVMF_VARS_MS", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd")
	for _, f := range []string{code, varsTemplate} {
		if _, err := os.Stat(f); err != nil {
			failOrSkipUnsupportedHost(t, "OVMF missing: %v", err)
		}
	}
	vm := &ukiVM{t: t, dir: dir, code: code, vars: filepath.Join(dir, "OVMF_VARS.fd"), disk: filepath.Join(dir, "disk.qcow2")}
	copyBinary(t, varsTemplate, vm.vars)
	setLUKSSecureBoot(t, vm.vars, false)
	run(t, "create disk", "qemu-img", "create", "-f", "qcow2", vm.disk, "6G")
	return vm
}

func (vm *ukiVM) args(extra ...string) []string {
	vm.boots++
	sock := startLUKSSWTPM(vm.t, filepath.Join(vm.dir, "tpm"), filepath.Join(vm.dir, fmt.Sprintf("tpm%d.sock", vm.boots)))
	return append([]string{
		"-machine", "q35,smm=on,accel=kvm:tcg", "-cpu", mirrorQEMUCPUModel(), "-m", "2048", "-smp", "2",
		"-nographic", "-no-reboot",
		"-global", "driver=cfi.pflash01,property=secure,value=on",
		"-drive", "if=pflash,format=raw,unit=0,readonly=on,file=" + vm.code,
		"-drive", "if=pflash,format=raw,unit=1,file=" + vm.vars,
		"-drive", "file=" + vm.disk + ",format=qcow2,if=virtio",
		"-chardev", "socket,id=chrtpm,path=" + sock,
		"-tpmdev", "emulator,id=tpm0,chardev=chrtpm", "-device", "tpm-tis,tpmdev=tpm0",
	}, extra...)
}

// provision runs BOOTy with vars and the cosign key file.
func (vm *ukiVM) provision(vars map[string]string, files map[string]string) []byte {
	initramfs := buildProvisionInitramfsWithFiles(vm.t, vars, files)
	out := runMirrorQEMU(vm.t, "provision", vm.args("-kernel", findKernel(vm.t), "-initrd", initramfs,
		"-append", runnerKernelCmdline+" net.ifnames=0", "-net", "nic,model=virtio", "-net", "user"), 20*time.Minute)
	vm.logs = append(vm.logs, out)
	if !strings.Contains(string(out), "step=report-success") || strings.Contains(string(out), "provisioning step failed") {
		vm.t.Fatalf("provisioning did not succeed. tail:\n%s", tail(out, 6000))
	}
	return out
}

// boot boots the installed system and returns its check report and whether
// the passphrase prompt came (answered with key, to read the report).
func (vm *ukiVM) boot(phase string, key []byte) (string, bool) {
	out, prompted := runQEMUAnswerPrompt(vm.t, phase, vm.args("-net", "none"), luksPrompt, key, 10*time.Minute)
	vm.logs = append(vm.logs, out)
	return luksCheckReport(vm.t, phase, out), prompted
}

// espFile reads or replaces a file on the ESP of the installed disk.
func (vm *ukiVM) espFile(espPart int, rel string, data []byte) []byte {
	mnt, cleanup := mountQcow2Partition(vm.t, vm.disk, espPart)
	defer cleanup()
	p := filepath.Join(mnt, rel)
	if data != nil {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			vm.t.Fatal(err)
		}
		return nil
	}
	return readFile(vm.t, p)
}

// enrollDB adds a Secure Boot certificate (PEM) to db, as a provisioner
// does over Redfish.
func (vm *ukiVM) enrollDB(certPEM []byte) {
	p := filepath.Join(vm.dir, fmt.Sprintf("db-%d.pem", vm.boots))
	if err := os.WriteFile(p, certPEM, 0o644); err != nil {
		vm.t.Fatal(err)
	}
	run(vm.t, "enroll the UKI certificate in db", "virt-fw-vars", "--inplace", vm.vars,
		"--add-db", "a0baa8a3-041d-48a8-bc87-c36d121b5e3d", p)
}

// assertNoSecrets fails if a secret is in any console log.
func (vm *ukiVM) assertNoSecrets(secrets ...[]byte) {
	for i, l := range vm.logs {
		for _, s := range secrets {
			if len(s) > 0 && bytes.Contains(l, s) {
				vm.t.Fatalf("a secret is in the console log of boot %d", i+1)
			}
		}
		if bytes.Contains(l, []byte("PRIVATE KEY")) {
			vm.t.Fatalf("a private key is in the console log of boot %d", i+1)
		}
	}
}

func ukiAssets(t *testing.T) (osImage string, debs []string) {
	t.Helper()
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	osImage = os.Getenv("MIRROR_OS_IMAGE")
	if _, err := os.Stat(osImage); osImage == "" || err != nil {
		failOrSkipUnsupportedHost(t, "MIRROR_OS_IMAGE missing: %q %v", osImage, err)
	}
	for _, tool := range []string{"virt-fw-vars", "swtpm", "cryptsetup", "qemu-nbd"} {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}
	return osImage, strings.Fields(os.Getenv("DISK_ENCRYPTION_DEBS"))
}

func ukiProvisionVars(imageRef, escrowURL, signing string) map[string]string {
	// The escrow server is HTTPS with a test CA and wants the bearer token.
	return map[string]string{
		"TOKEN":                            "e2e-token",
		"CA_BUNDLE_FILE":                   "/" + luksCAPath,
		"HOSTNAME":                         "luks-e2e",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"IMAGE":                            imageRef,
		"IMAGE_COSIGN_PUBKEYS":             "/" + cosignTestKeyPath,
		"DISK_ENCRYPTION":                  "luks2-tpm2-uki",
		"SECUREBOOT_SIGNING":               signing,
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrowURL + "/recovery-key",
	}
}

// requireBound checks the state after the first-boot enrollment.
func requireUKIBound(t *testing.T, report string) {
	t.Helper()
	for _, line := range []string{"SECUREBOOT=1", "STUB=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "TPM2_PUBKEY_PCRS=11",
		"TPM2_HASH_PCRS=7", "EMPTY_PASS=refused", "UNIT=disabled"} {
		luksRequireLine(t, report, line)
	}
	if strings.HasPrefix(luksField(report, "KEYSLOTS"), "0,1,") {
		t.Fatalf("key slots %q: the empty passphrase slot 1 is still there", luksField(report, "KEYSLOTS"))
	}
}

// TestDiskEncryptionUKIPerHostVM: the image ships an unsigned UKI. BOOTy
// signs it and its PCR 11 policy with per-host keys that it wipes, and
// reports the certificate, which the test enrolls in db like a
// provisioner. The first Secure Boot boot binds the volume to PCR 7 and the
// signed PCR 11 policy; the next unlocks with the TPM2 alone; with Secure
// Boot off the TPM2 refuses and the recovery key unlocks.
func TestDiskEncryptionUKIPerHostVM(t *testing.T) {
	osImage, debs := ukiAssets(t)
	dir := t.TempDir()
	img := prepareUKIImage(t, osImage, dir, debs, nil, nil)
	ref, cosignPEM := pushUKIImage(t, img)
	escrow := startEscrowServer(t)
	vm := newUKIVM(t, dir)

	out := vm.provision(ukiProvisionVars(ref, escrow.url, "per-host"), map[string]string{cosignTestKeyPath: cosignPEM, luksCAPath: escrow.caPEM})
	for _, want := range []string{"image cosign signature verified", "UKI signed with per-host keys; the private keys are wiped", "UKI boot entry created"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("provisioning log lacks %q", want)
		}
	}
	key := escrow.recoveryKey(t)
	cert := vm.espFile(img.espPart, "EFI/BOOTy/uki-signing.crt", nil)
	if got := sha256.Sum256(vm.espFile(img.espPart, "EFI/Linux/"+img.ukiName, nil)); hex.EncodeToString(got[:]) == img.ukiSHA {
		t.Fatal("per-host mode left the unsigned UKI on the ESP")
	}
	vm.enrollDB(cert)

	// 1. First Secure Boot boot: the empty passphrase opens, the unit binds.
	setLUKSSecureBoot(t, vm.vars, true)
	report, prompted := vm.boot("first secure boot", key)
	if prompted {
		t.Fatal("passphrase prompt on the first boot: the empty passphrase did not open the volume")
	}
	requireUKIBound(t, report)

	// 2. The TPM2 unlocks without a prompt.
	report, prompted = vm.boot("tpm2 unlock", key)
	if prompted {
		t.Fatal("the TPM2 did not unlock the per-host signed UKI")
	}
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	pcr7 := luksField(report, "PCR7")

	// 3. Secure Boot off: PCR 7 differs, the recovery key unlocks.
	setLUKSSecureBoot(t, vm.vars, false)
	report, prompted = vm.boot("secure boot off", key)
	if !prompted {
		t.Fatal("no passphrase prompt with Secure Boot off")
	}
	luksRequireLine(t, report, "SECUREBOOT=0")
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	if pcr7 == "" || pcr7 == luksField(report, "PCR7") {
		t.Fatalf("PCR 7 did not change with the Secure Boot state: %q", pcr7)
	}
	vm.assertNoSecrets(key, bytes.ReplaceAll(key, []byte("-"), nil))
}

// TestDiskEncryptionUKICIVM: the image pipeline signs the UKI and its PCR
// 11 policy. BOOTy makes no key. A UKI update signed with the same keys
// unlocks without re-enrollment; a UKI whose PCR 11 policy is signed by
// another key falls back to the recovery key; an older validly signed UKI
// still unlocks (no rollback protection, docs/disk-encryption.md).
//
//nolint:funlen // one VM life cycle, phase by phase
func TestDiskEncryptionUKICIVM(t *testing.T) {
	osImage, debs := ukiAssets(t)
	dir := t.TempDir()
	ci := newUKIKeys(t, dir, "ci-")
	rogue := newUKIKeys(t, dir, "rogue-")
	img := prepareUKIImage(t, osImage, dir, debs, &ci, map[string]ukiVariant{
		"update":   {marker: "v2", pcr: ci, sb: ci},
		"tampered": {marker: "v3", pcr: rogue, sb: ci},
	})
	ref, cosignPEM := pushUKIImage(t, img)
	escrow := startEscrowServer(t)
	vm := newUKIVM(t, dir)

	vars := ukiProvisionVars(ref, escrow.url, "ci")
	vars["DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE"] = "/deploy/pcr.pub"
	out := vm.provision(vars, map[string]string{cosignTestKeyPath: cosignPEM, luksCAPath: escrow.caPEM, "deploy/pcr.pub": string(readFile(t, ci.pcrPub))})
	if strings.Contains(string(out), "UKI signed with per-host keys") || !strings.Contains(string(out), "UKI prepared") {
		t.Fatal("ci mode signed the UKI or did not prepare it")
	}
	key := escrow.recoveryKey(t)
	if got := sha256.Sum256(vm.espFile(img.espPart, "EFI/Linux/"+img.ukiName, nil)); hex.EncodeToString(got[:]) != img.ukiSHA {
		t.Fatal("ci mode changed the UKI on the ESP")
	}
	vm.enrollDB(readFile(t, ci.sbCert))
	setLUKSSecureBoot(t, vm.vars, true)

	// 1. First Secure Boot boot binds.
	report, prompted := vm.boot("first secure boot", key)
	if prompted {
		t.Fatal("passphrase prompt on the first boot")
	}
	requireUKIBound(t, report)
	luksRequireLine(t, report, "CMDLINE=")
	if !strings.Contains(luksField(report, "CMDLINE"), "booty.uki=v1") {
		t.Fatalf("not booted from the image UKI: %s", luksField(report, "CMDLINE"))
	}
	token := luksField(report, "TOKEN_SUM")

	// 2. A kernel/UKI update signed with the same keys unlocks; the token
	// is unchanged, so nothing was enrolled again.
	vm.espFile(img.espPart, "EFI/Linux/"+img.ukiName, readFile(t, img.variants["update"]))
	report, prompted = vm.boot("updated uki", key)
	if prompted {
		t.Fatal("the TPM2 did not unlock the updated UKI")
	}
	if !strings.Contains(luksField(report, "CMDLINE"), "booty.uki=v2") || luksField(report, "TOKEN_SUM") != token {
		t.Fatalf("cmdline %q token %q (was %q)", luksField(report, "CMDLINE"), luksField(report, "TOKEN_SUM"), token)
	}

	// 3. A UKI that Secure Boot accepts but whose PCR 11 policy is signed
	// by another key: the TPM2 refuses, the recovery key unlocks.
	vm.espFile(img.espPart, "EFI/Linux/"+img.ukiName, readFile(t, img.variants["tampered"]))
	report, prompted = vm.boot("tampered uki", key)
	if !prompted {
		t.Fatal("no passphrase prompt for a PCR 11 policy signed by another key")
	}
	if !strings.Contains(luksField(report, "CMDLINE"), "booty.uki=v3") {
		t.Fatalf("not booted from the tampered UKI: %s", luksField(report, "CMDLINE"))
	}
	luksRequireLine(t, report, "ROOT_TYPE=crypt")

	// 4. The older v1 UKI, validly signed, still unlocks: a signed PCR 11
	// policy alone has no rollback protection.
	vm.espFile(img.espPart, "EFI/Linux/"+img.ukiName, readFile(t, img.variants["v1"]))
	_, prompted = vm.boot("older uki", key)
	if prompted {
		t.Fatal("expected the older signed UKI to unlock (documented limitation)")
	}
	vm.assertNoSecrets(key, bytes.ReplaceAll(key, []byte("-"), nil))
}
