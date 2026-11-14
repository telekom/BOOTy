//go:build e2e

package kvm

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/secureboot"
)

const (
	modCheckBegin = "BOOTY-MODSIGN-CHECK-BEGIN"
	modCheckEnd   = "BOOTY-MODSIGN-CHECK-END"
	// shimLockGUID owns MokList and MokListRT.
	shimLockGUID = "605dab50-e046-4300-abb6-3dd810dd8b23"
)

// modCheckScript runs in the installed OS on every boot. It reports the
// Secure Boot state, the BOOTy key in the kernel keyrings, PCR 7 and 14, and
// tries to load three modules: one BOOTy signed in updates/, one unsigned,
// and one signed with the key of another host.
const modCheckScript = `#!/bin/sh
exec >/dev/ttyS0 2>&1
. /etc/booty-e2e/modules.env
echo "` + modCheckBegin + `"
sb=$(od -An -tu1 -j4 -N1 /sys/firmware/efi/efivars/SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c | tr -d ' ')
echo "SECUREBOOT=$sb"
echo "KERNEL=$(uname -r)"
echo "PACKAGES=$(dpkg-query -W -f='${Package}=${Version} ' shim-signed shim 2>/dev/null)"
echo "MOKTRUSTED=$(od -An -tu1 -j4 /sys/firmware/efi/efivars/MokListTrustedRT-605dab50-e046-4300-abb6-3dd810dd8b23 2>/dev/null | tr -d ' ')"
echo "LOCKDOWN=$(cat /sys/kernel/security/lockdown 2>/dev/null)"
grep -i -e 'BOOTy module signing' -e 'CI module signing' /proc/keys | sed 's/^/KEY /'
grep -E ' \.(machine|platform|secondary_trusted_keys|builtin_trusted_keys): ' /proc/keys | sed 's/^/KEYRING /'
echo "PCR7=$(cat /sys/class/tpm/tpm0/pcr-sha256/7 2>/dev/null)"
echo "PCR14=$(cat /sys/class/tpm/tpm0/pcr-sha256/14 2>/dev/null)"
try() {
  if out=$($1 2>&1); then echo "$2=loaded"; else echo "$2=refused $out"; fi
}
try "modprobe $SIGNED" SIGNED_MODULE
echo "SIGNED_SIGNER=$(modinfo -F signer $SIGNED)"
try "insmod /opt/booty-e2e/$UNSIGNED.ko" UNSIGNED_MODULE
try "insmod /opt/booty-e2e/$OTHERKEY.ko" OTHERKEY_MODULE
echo "SYSEXT_MERGED=$(systemd-sysext status 2>/dev/null | grep -c booty-modules)"
journalctl -b -u systemd-sysext --no-pager -o cat 2>/dev/null | tail -n 5 | sed 's/^/SYSEXTLOG /'
dmesg | grep -i -e 'module verification' -e 'Lockdown' -e 'Loaded X.509 cert .BOOTy' -e 'verity' | sed 's/^/DMESG /'
echo "` + modCheckEnd + `"
systemctl poweroff --no-block
`

const modCheckUnit = `[Unit]
Description=BOOTy module signing e2e check
After=local-fs.target
Before=serial-getty@ttyS0.service

[Service]
Type=oneshot
ExecStart=/bin/sh /usr/local/lib/booty-modsign-check.sh

[Install]
WantedBy=multi-user.target
`

// modSignVM is the KVM setup of the module signing scenarios: an Ubuntu
// cloud image (MIRROR_OS_IMAGE) with the distribution shim and kernel, OVMF
// with the Microsoft keys and Secure Boot off, swtpm, a raw disk, and a
// provisioning ramdisk with mok-enroll.efi (BOOTY_MOK_ENROLL_EFI) at the
// path of the release ramdisks.
type modSignVM struct {
	t                                     *testing.T
	dir, code, vars, disk, kernel, initrd string
	boots                                 int
}

// requireModSignHost returns MIRROR_OS_IMAGE after checking the assets and
// tools of the module signing scenarios.
func requireModSignHost(t *testing.T) string {
	t.Helper()
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	osImage := os.Getenv("MIRROR_OS_IMAGE")
	for _, f := range []string{osImage, os.Getenv("BOOTY_MOK_ENROLL_EFI"),
		envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd"),
		envOrDefault("OVMF_VARS_MS", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd")} {
		if _, err := os.Stat(f); f == "" || err != nil {
			failOrSkipUnsupportedHost(t, "module signing asset missing (MIRROR_OS_IMAGE, BOOTY_MOK_ENROLL_EFI, OVMF): %q %v", f, err)
		}
	}
	for _, tool := range []string{"virt-fw-vars", "swtpm", "zstd", "depmod"} {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}
	return osImage
}

func newModSignVM(t *testing.T, dir, gzImage string, vars map[string]string) *modSignVM {
	t.Helper()
	helperEFI, err := os.ReadFile(os.Getenv("BOOTY_MOK_ENROLL_EFI"))
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     startImageServer(t, gzImage) + "/image.gz",
		"MODE":                      "provision",
		"MIN_DISK_SIZE_GB":          "1",
	}
	for k, v := range vars {
		all[k] = v
	}
	m := &modSignVM{
		t: t, dir: dir,
		code:   envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd"),
		vars:   filepath.Join(dir, "OVMF_VARS.fd"),
		disk:   filepath.Join(dir, "disk.raw"),
		kernel: findKernel(t),
		initrd: buildProvisionInitramfsWithFiles(t, all, map[string]string{"usr/lib/booty/mok-enroll.efi": string(helperEFI)}),
	}
	copyBinary(t, envOrDefault("OVMF_VARS_MS", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd"), m.vars)
	setSecureBoot(t, m.vars, false)
	// Raw, so a test can mount the provisioned disk between boots.
	run(t, "create disk", "qemu-img", "create", "-f", "raw", m.disk, "6G")
	return m
}

// args returns the QEMU arguments of the next boot; swtpm exits when QEMU
// disconnects, so every boot gets a new one.
func (m *modSignVM) args(extra ...string) []string {
	m.boots++
	tpm := startSWTPM(m.t, filepath.Join(m.dir, fmt.Sprintf("tpm%d", m.boots)))
	return append([]string{
		"-machine", "q35,smm=on,accel=kvm:tcg", "-cpu", mirrorQEMUCPUModel(), "-m", "2048", "-smp", "2",
		"-nographic", "-no-reboot",
		"-global", "driver=cfi.pflash01,property=secure,value=on",
		"-drive", "if=pflash,format=raw,unit=0,readonly=on,file=" + m.code,
		"-drive", "if=pflash,format=raw,unit=1,file=" + m.vars,
		"-drive", "file=" + m.disk + ",format=raw,if=virtio",
		"-chardev", "socket,id=chrtpm,path=" + tpm,
		"-tpmdev", "emulator,id=tpm0,chardev=chrtpm", "-device", "tpm-tis,tpmdev=tpm0",
	}, extra...)
}

// provision runs the BOOTy ramdisk and returns its console output.
func (m *modSignVM) provision(phase string) string {
	m.t.Helper()
	out := string(runMirrorQEMU(m.t, phase, m.args("-kernel", m.kernel, "-initrd", m.initrd,
		"-append", runnerKernelCmdline+" net.ifnames=0", "-net", "nic,model=virtio", "-net", "user"), 10*time.Minute))
	if !strings.Contains(out, "step=report-success") || strings.Contains(out, "provisioning step failed") {
		m.t.Fatalf("%s did not succeed", phase)
	}
	// Provisioning leaves gigabytes of the disk image in the host page cache.
	// Writing them back here keeps the writeback from stalling the next QEMU
	// (its pflash writes) for minutes.
	start := time.Now()
	run(m.t, "sync after "+phase, "sync")
	m.t.Logf("sync after %s took %s", phase, time.Since(start).Round(time.Second))
	return out
}

// enroll boots the firmware once: BootNext runs mok-enroll.efi.
func (m *modSignVM) enroll(phase string) {
	m.t.Helper()
	out := runQEMUUntilReset(m.t, phase, m.args("-net", "none"), 3*time.Minute, "BOOTy mok-enroll: MokList updated")
	if !strings.Contains(string(out), "BOOTy mok-enroll: MokList updated") {
		m.t.Fatalf("%s: mok-enroll.efi did not run:\n%s", phase, tail(out, 4000))
	}
}

// syncBuffer is a bytes.Buffer that QEMU writes and the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// runQEMUUntilReset is runMirrorQEMU for a boot that ends with a firmware
// reset after the guest printed done. On the CI runners QEMU sometimes does
// not exit on that reset (-no-reboot) within minutes, although the variable
// store is already written: pflash writes go to the file at once. Thirty
// seconds after done, the test stops QEMU and goes on.
func runQEMUUntilReset(t *testing.T, phase string, args []string, timeout time.Duration, done string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out syncBuffer
	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("QEMU %s: %v", phase, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var doneAt time.Time
	for tick := time.NewTicker(time.Second); ; {
		select {
		case err := <-exited:
			tick.Stop()
			if ctx.Err() != nil {
				t.Fatalf("QEMU %s timed out after %s. output:\n%s", phase, timeout, readableSerial(out.Bytes()))
			}
			t.Logf("QEMU %s exited (%v). output:\n%s", phase, err, readableSerial(out.Bytes()))
			return out.Bytes()
		case <-tick.C:
			if doneAt.IsZero() && bytes.Contains(out.Bytes(), []byte(done)) {
				doneAt = time.Now()
			}
			if !doneAt.IsZero() && time.Since(doneAt) > 30*time.Second {
				t.Logf("QEMU %s did not exit on the reset after %q; stopping it", phase, done)
				_ = cmd.Process.Kill()
				<-exited
				tick.Stop()
				return out.Bytes()
			}
		}
	}
}

// check boots the installed system and returns the report of the check
// service.
func (m *modSignVM) check(phase string) string {
	m.t.Helper()
	return modCheckReport(m.t, phase, runMirrorQEMU(m.t, phase, m.args("-net", "none"), 8*time.Minute))
}

// TestModuleSigningSecureBootVM provisions with SECUREBOOT_SIGNING=per-host,
// lets mok-enroll.efi write MokList on the next boot, turns Secure Boot on
// (as a BMC would) and boots the installed system through shim. The BOOTy
// signed module must load; an unsigned module and one signed by another
// host's key must be refused. With Secure Boot off again the system must
// still boot. A second provisioning must replace the certificate in MokList
// instead of adding one.
func TestModuleSigningSecureBootVM(t *testing.T) {
	dir := t.TempDir()
	gzImage, _ := prepareModuleImage(t, requireModSignHost(t), dir)
	m := newModSignVM(t, dir, gzImage, map[string]string{"HOSTNAME": "modsign-e2e", "SECUREBOOT_SIGNING": "per-host"})
	vars := m.vars

	// Provisioning with Secure Boot off, then the BootNext boot of the helper.
	m.provision("provision")
	m.enroll("mok-enroll")
	certs := bootyMOKCerts(t, vars)
	if len(certs) != 1 {
		t.Fatalf("MokList holds %d BOOTy certificates, want 1", len(certs))
	}

	// Secure Boot on: shim mirrors MokList, the kernel trusts the key.
	setSecureBoot(t, vars, true)
	report := m.check("secure boot on")
	requireLine(t, report, "SECUREBOOT=1")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "SIGNED_SIGNER=BOOTy module signing modsign-e2e")
	requireLine(t, report, "UNSIGNED_MODULE=refused")
	requireLine(t, report, "OTHERKEY_MODULE=refused")
	pcr7On := field(report, "PCR7")

	// Secure Boot off: the system still boots, unsigned modules load.
	setSecureBoot(t, vars, false)
	report = m.check("secure boot off")
	requireLine(t, report, "SECUREBOOT=0")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "UNSIGNED_MODULE=loaded")
	if pcr7On == "" || pcr7On == field(report, "PCR7") {
		t.Fatalf("PCR 7 did not change with the Secure Boot state: %q", pcr7On)
	}

	// Reprovisioning replaces the old certificate.
	m.provision("reprovision")
	m.enroll("mok-enroll after reprovision")
	again := bootyMOKCerts(t, vars)
	if len(again) != 1 || again[0] == certs[0] {
		t.Fatalf("after reprovisioning MokList holds %d BOOTy certificates (same as before: %v), want one new", len(again), len(again) == 1)
	}

	// Option (b): the certificate in db only, as a BMC would enroll it over
	// Redfish. The kernel keeps db certificates in the .platform keyring;
	// Debian and Ubuntu kernels carry a patch that verifies modules with it
	// too, upstream kernels use it for kexec only.
	der, err := hex.DecodeString(again[0])
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "booty.der")
	if err := os.WriteFile(certFile, der, 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, "move the certificate from MokList to db", "virt-fw-vars", "--inplace", vars,
		"-d", "MokList", "--add-db", secureboot.MOKOwnerGUID, certFile, "--set-true", "SecureBootEnable")
	if left := bootyMOKCerts(t, vars); len(left) != 0 {
		t.Fatalf("MokList still holds %d BOOTy certificates", len(left))
	}
	report = m.check("db only")
	requireLine(t, report, "SECUREBOOT=1")
	requireLine(t, report, "UNSIGNED_MODULE=refused")
	if field(report, "SIGNED_MODULE") == "loaded" {
		t.Log("db only: the kernel accepts modules signed by a db certificate (distribution patch)")
	} else {
		t.Log("db only: the kernel refuses modules signed by a db certificate (upstream behavior)")
	}
}

// TestModuleSigningCIVM provisions an image whose module CI signed, with
// SECUREBOOT_SIGNING=ci and the CI certificate in MOK_CERT_PATH. BOOTy must
// not sign anything or hold a key; it enrolls the CI certificate, and with
// Secure Boot on the CI-signed module loads.
func TestModuleSigningCIVM(t *testing.T) {
	dir := t.TempDir()
	const ciCert = "/usr/share/booty-e2e/ci-mok.der"
	var ci *secureboot.SignResult
	gzImage, mods := prepareModuleImage(t, requireModSignHost(t), dir, func(mnt, kernel string) {
		var err error
		// CI signs the module in the image build and ships its certificate.
		if ci, err = secureboot.SignModuleTree(mnt, "CI module signing e2e"); err != nil {
			t.Fatal(err)
		}
		writeImageFile(t, mnt, strings.TrimPrefix(ciCert, "/"), ci.CertDER)
		run(t, "depmod in the image", "depmod", "-b", mnt, kernel)
	})
	m := newModSignVM(t, dir, gzImage, map[string]string{
		"HOSTNAME": "modci-e2e", "SECUREBOOT_SIGNING": "ci", "MOK_CERT_PATH": ciCert,
	})
	out := m.provision("provision")
	if !strings.Contains(out, "CI module signing certificate to enroll") || strings.Contains(out, "signed out-of-tree kernel modules") {
		t.Fatal("signing ci did not just read the CI certificate")
	}
	m.enroll("mok-enroll")
	if certs := bootyMOKCerts(t, m.vars); len(certs) != 1 || certs[0] != hex.EncodeToString(ci.CertDER) {
		t.Fatalf("MokList holds %d BOOTy certificates, want the CI certificate", len(certs))
	}
	setSecureBoot(t, m.vars, true)
	report := m.check("secure boot on")
	requireLine(t, report, "SECUREBOOT=1")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "SIGNED_SIGNER=CI module signing e2e")
	requireLine(t, report, "UNSIGNED_MODULE=refused")
	requireLine(t, report, "OTHERKEY_MODULE=refused")
	t.Logf("CI-signed module %s loads with the CI certificate enrolled", mods[0].name)
}

// TestModuleSigningMokutilVM provisions with MODULE_SIGNING_ENROLLMENT=mokutil:
// BOOTy queues MokNew and MokAuth. With Secure Boot on, shim starts
// MokManager, and the test confirms the request at the serial console like
// an operator at the BMC console, with MOK_PASSWORD. MokManager must accept
// the password (MokAuth), write the certificate to MokList, and the BOOTy
// signed module must load.
func TestModuleSigningMokutilVM(t *testing.T) {
	dir := t.TempDir()
	const password = "booty-mok-e2e"
	gzImage, _ := prepareModuleImage(t, requireModSignHost(t), dir)
	m := newModSignVM(t, dir, gzImage, map[string]string{
		"HOSTNAME": "modmok-e2e", "SECUREBOOT_SIGNING": "per-host",
		"MODULE_SIGNING_ENROLLMENT": "mokutil", "MOK_PASSWORD": password,
	})
	out := m.provision("provision")
	if !strings.Contains(out, "MOK enrollment queued") || strings.Contains(out, "MOK enrollment staged") {
		t.Fatal("enrollment mokutil did not queue the MOK request")
	}
	pending := map[string]bool{}
	for _, v := range efiVariables(t, m.vars) {
		if v.GUID == shimLockGUID {
			pending[v.Name] = true
		}
	}
	if !pending["MokNew"] || !pending["MokAuth"] {
		t.Fatalf("MokNew and MokAuth not queued: %v", pending)
	}
	if certs := bootyMOKCerts(t, m.vars); len(certs) != 0 {
		t.Fatalf("MokList holds %d BOOTy certificates before MokManager ran", len(certs))
	}

	setSecureBoot(t, m.vars, true)
	m.mokManagerEnroll("mokmanager", password)
	if certs := bootyMOKCerts(t, m.vars); len(certs) != 1 {
		t.Fatalf("MokList holds %d BOOTy certificates after MokManager, want 1", len(certs))
	}
	for _, v := range efiVariables(t, m.vars) {
		if v.GUID == shimLockGUID && (v.Name == "MokNew" || v.Name == "MokAuth") {
			t.Fatalf("MokManager left %s", v.Name)
		}
	}
	report := m.check("secure boot on")
	requireLine(t, report, "SECUREBOOT=1")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "SIGNED_SIGNER=BOOTy module signing modmok-e2e")
	requireLine(t, report, "UNSIGNED_MODULE=refused")
}

// mokManagerEnroll boots shim with a pending MOK request and answers
// MokManager on the serial console: any key, Enroll MOK, Continue, Yes, the
// password, then Reboot (or Continue boot, where a MokManager version offers
// that), until QEMU exits on the reset.
func (m *modSignVM) mokManagerEnroll(phase, password string) {
	m.t.Helper()
	t := m.t
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var out syncBuffer
	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", m.args("-net", "none")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("QEMU %s: %v", phase, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ansi := regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	const down = "\x1b[B"
	steps := []struct{ wait, send string }{
		{"Press any key", " "},
		{"Enroll MOK", down + "\r"},
		{"View key 0", down + "\r"},     // View key 0, Continue
		{"Enroll the key", down + "\r"}, // No, Yes
		{"Password", password + "\r"},
		{"Reboot", "\r"},
	}
	from, step := 0, 0
	var doneAt time.Time
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			text := readableSerial(out.Bytes())
			if ctx.Err() != nil {
				t.Fatalf("QEMU %s timed out after step %d. output:\n%s", phase, step, text)
			}
			if step < len(steps)-1 {
				t.Fatalf("QEMU %s exited (%v) before MokManager asked for %q. output:\n%s", phase, err, steps[step].wait, text)
			}
			t.Logf("QEMU %s exited (%v). output:\n%s", phase, err, text)
			return
		case <-tick.C:
			if step == len(steps) {
				// The reset does not always end QEMU (runQEMUUntilReset);
				// MokList is in the variable store by then.
				if time.Since(doneAt) > 30*time.Second {
					t.Logf("QEMU %s did not exit on the reset; stopping it. output:\n%s", phase, readableSerial(out.Bytes()))
					_ = cmd.Process.Kill()
					<-exited
					return
				}
				continue
			}
			text := ansi.ReplaceAllString(string(out.Bytes()), "")
			i := strings.Index(text[min(from, len(text)):], steps[step].wait)
			if i < 0 && step == len(steps)-1 {
				i = strings.Index(text[min(from, len(text)):], "Continue boot")
			}
			if i < 0 {
				continue
			}
			from += i + len(steps[step].wait)
			// MokManager redraws the screen; let it settle before the keys.
			time.Sleep(time.Second)
			if _, err := stdin.Write([]byte(steps[step].send)); err != nil {
				t.Fatalf("QEMU %s: send keys: %v", phase, err)
			}
			step++
			doneAt = time.Now()
		}
	}
}

// TestModuleSigningOffVM is the regression case for the default: without
// SECUREBOOT_SIGNING, with mok-enroll.efi in the ramdisk and Secure Boot
// off, provisioning must leave the module, the ESP and the EFI variables as
// without the feature, and the installed system boots.
func TestModuleSigningOffVM(t *testing.T) {
	dir := t.TempDir()
	var modPath string
	var modBefore []byte
	gzImage, mods := prepareModuleImage(t, requireModSignHost(t), dir, func(mnt, kernel string) {
		run(t, "depmod in the image", "depmod", "-b", mnt, kernel)
		modPath = filepath.Join("usr/lib/modules", kernel, "updates/booty-e2e")
	})
	modPath = filepath.Join(modPath, mods[0].name+".ko")
	modBefore = mods[0].raw
	m := newModSignVM(t, dir, gzImage, map[string]string{"HOSTNAME": "modoff-e2e"})
	out := m.provision("provision")
	for _, s := range []string{"signed out-of-tree kernel modules", "MOK enrollment staged", "CI module signing certificate"} {
		if strings.Contains(out, s) {
			t.Fatalf("signing off logged %q", s)
		}
	}
	withDiskPartition(t, m.disk, dir, "ext4", func(mnt string) {
		if got, err := os.ReadFile(filepath.Join(mnt, modPath)); err != nil || !bytes.Equal(got, modBefore) {
			t.Fatalf("the module changed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(mnt, "usr/share/booty/secureboot")); err == nil {
			t.Fatal("a module signing certificate was installed")
		}
	})
	withDiskPartition(t, m.disk, dir, "vfat", func(mnt string) {
		if _, err := os.Stat(filepath.Join(mnt, "EFI/BOOTy/mok-enroll.efi")); err == nil {
			t.Fatal("mok-enroll.efi was copied to the ESP")
		}
	})
	for _, v := range efiVariables(t, m.vars) {
		data, _ := hex.DecodeString(v.Data)
		if v.Name == "BootNext" || v.Name == "BOOTyMokEnroll" || v.Name == "MokList" ||
			bytes.Contains(data, utf16le("booty-mok-enroll")) {
			t.Fatalf("signing off left EFI variable %s", v.Name)
		}
	}
	report := m.check("installed system")
	requireLine(t, report, "SECUREBOOT=0")
	requireLine(t, report, "SIGNED_MODULE=loaded")
}

type efiVariable struct {
	Name string `json:"name"`
	GUID string `json:"guid"`
	Attr int    `json:"attr"`
	Data string `json:"data"`
}

func efiVariables(t *testing.T, vars string) []efiVariable {
	t.Helper()
	out := filepath.Join(t.TempDir(), "vars.json")
	run(t, "dump variables", "virt-fw-vars", "-i", vars, "--output-json", out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var dump struct {
		Variables []efiVariable `json:"variables"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.Variables
}

func utf16le(s string) []byte {
	out := make([]byte, 0, 2*len(s))
	for _, r := range s {
		out = append(out, byte(r), 0)
	}
	return out
}

// withDiskPartition mounts the first partition of the raw disk with file
// system fstype while f runs.
func withDiskPartition(t *testing.T, disk, dir, fstype string, f func(mnt string)) {
	t.Helper()
	loop := strings.TrimSpace(string(runOutput(t, "attach disk", "losetup", "-P", "--show", "-f", disk)))
	defer func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() }()
	parts, _ := filepath.Glob(loop + "p*")
	part := ""
	for _, p := range parts {
		if typ, err := exec.CommandContext(t.Context(), "blkid", "-o", "value", "-s", "TYPE", p).Output(); err == nil && strings.TrimSpace(string(typ)) == fstype {
			part = p
			break
		}
	}
	if part == "" {
		t.Fatalf("no %s partition on %s (%v)", fstype, disk, parts)
	}
	mnt := filepath.Join(dir, "mnt-"+fstype)
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "mount "+part, "mount", part, mnt)
	defer func() { _ = exec.CommandContext(context.Background(), "umount", mnt).Run() }()
	f(mnt)
	run(t, "unmount "+part, "umount", mnt)
}

// prepareModuleImage installs the check service and three test modules into
// the root file system of the cloud image and returns the gzipped raw image
// and the test modules (signed, unsigned, other key). extra runs with the
// image root mounted at mnt.
func prepareModuleImage(t *testing.T, osImage, dir string, extra ...func(mnt, kernel string)) (string, []testModule) {
	t.Helper()
	raw := filepath.Join(dir, "os.raw")
	run(t, "convert OS image to raw", "qemu-img", "convert", "-O", "raw", osImage, raw)
	loop := strings.TrimSpace(string(runOutput(t, "attach image", "losetup", "-P", "--show", "-f", raw)))
	mnt := filepath.Join(dir, "mnt")
	// Best effort on failure; the success path below unmounts and detaches.
	defer func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() }()
	defer func() { _ = exec.CommandContext(context.Background(), "umount", mnt).Run() }()
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "mount image root", "mount", loop+"p1", mnt)

	kernels, err := filepath.Glob(filepath.Join(mnt, "lib/modules/*"))
	if err != nil || len(kernels) != 1 {
		t.Fatalf("want one kernel in the image: %v %v", kernels, err)
	}
	names := pickTestModules(t, kernels[0], 3)
	signed, unsigned, otherKey := names[0], names[1], names[2]
	writeImageFile(t, mnt, filepath.Join("lib/modules", filepath.Base(kernels[0]), "updates/booty-e2e", signed.name+".ko"), signed.raw)
	writeImageFile(t, mnt, "opt/booty-e2e/"+unsigned.name+".ko", unsigned.raw)
	writeImageFile(t, mnt, "opt/booty-e2e/"+otherKey.name+".ko", signWithOtherHostKey(t, otherKey.raw))
	writeImageFile(t, mnt, "etc/booty-e2e/modules.env",
		[]byte("SIGNED="+signed.name+"\nUNSIGNED="+unsigned.name+"\nOTHERKEY="+otherKey.name+"\n"))
	writeImageFile(t, mnt, "usr/local/lib/booty-modsign-check.sh", []byte(modCheckScript))
	writeImageFile(t, mnt, "etc/systemd/system/booty-modsign-check.service", []byte(modCheckUnit))
	link := filepath.Join(mnt, "etc/systemd/system/multi-user.target.wants/booty-modsign-check.service")
	if err := os.Symlink("/etc/systemd/system/booty-modsign-check.service", link); err != nil {
		t.Fatal(err)
	}
	t.Logf("test modules: signed=%s unsigned=%s other-key=%s", signed.name, unsigned.name, otherKey.name)
	for _, f := range extra {
		f(mnt, filepath.Base(kernels[0]))
	}
	run(t, "unmount image root", "umount", mnt)
	run(t, "detach image", "losetup", "-d", loop)
	gz := compressGzip(t, raw)
	if err := os.Remove(raw); err != nil {
		t.Fatal(err)
	}
	return gz, names
}

type testModule struct {
	name string
	raw  []byte
}

// pickTestModules returns n in-tree network modules without dependencies,
// decompressed and with the distribution signature removed. Modules that
// udev loads at boot would make insmod fail with EEXIST, so the candidates
// are virtual devices.
func pickTestModules(t *testing.T, kernelDir string, n int) []testModule {
	t.Helper()
	var mods []testModule
	for _, name := range []string{"dummy", "ifb", "nlmon", "vcan", "vxcan", "netdevsim", "veth"} {
		top, _ := filepath.Glob(filepath.Join(kernelDir, "kernel/drivers/net", name+".ko*"))
		sub, _ := filepath.Glob(filepath.Join(kernelDir, "kernel/drivers/net/*", name+".ko*"))
		matches := append(top, sub...)
		if len(matches) == 0 {
			continue
		}
		data, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(matches[0], ".zst") {
			cmd := exec.CommandContext(t.Context(), "zstd", "-dc")
			cmd.Stdin = bytes.NewReader(data)
			if data, err = cmd.Output(); err != nil {
				t.Fatalf("zstd -d %s: %v", matches[0], err)
			}
		}
		raw, err := secureboot.StripModuleSignature(data)
		if err != nil {
			t.Fatal(err)
		}
		if !moduleHasNoDeps(raw) {
			continue
		}
		mods = append(mods, testModule{name: name, raw: raw})
		if len(mods) == n {
			return mods
		}
	}
	t.Fatalf("found %d dependency-free test modules in %s, want %d", len(mods), kernelDir, n)
	return nil
}

func moduleHasNoDeps(raw []byte) bool {
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	sec := f.Section(".modinfo")
	if sec == nil {
		return false
	}
	info, err := sec.Data()
	if err != nil {
		return false
	}
	for _, kv := range bytes.Split(info, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, []byte("depends=")); ok {
			return len(v) == 0
		}
	}
	return true
}

// signWithOtherHostKey signs raw with the per-host key of a different host.
func signWithOtherHostKey(t *testing.T, raw []byte) []byte {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "lib/modules/0/updates/m.ko")
	writeImageFile(t, root, "lib/modules/0/updates/m.ko", raw)
	if _, err := secureboot.SignModuleTree(root, "BOOTy module signing other-host"); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeImageFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// setSecureBoot sets SecureBootEnable in the variable store, like turning
// Secure Boot on or off in the firmware setup or over Redfish.
func setSecureBoot(t *testing.T, vars string, on bool) {
	t.Helper()
	flag := "--set-false"
	if on {
		flag = "--set-true"
	}
	run(t, "set SecureBootEnable", "virt-fw-vars", "--inplace", vars, flag, "SecureBootEnable")
}

// bootyMOKCerts returns the hex certificates in MokList owned by BOOTy.
func bootyMOKCerts(t *testing.T, vars string) []string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "vars.json")
	run(t, "dump variables", "virt-fw-vars", "-i", vars, "--output-json", out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var dump struct {
		Variables []struct {
			Name string `json:"name"`
			GUID string `json:"guid"`
			Attr int    `json:"attr"`
			Data string `json:"data"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatal(err)
	}
	owner, _ := hex.DecodeString("358d37110ee2164194d7758d7540a17f") // MOKOwnerGUID, little-endian
	for _, v := range dump.Variables {
		if v.Name != "MokList" || v.GUID != shimLockGUID {
			continue
		}
		if v.Attr != 3 {
			t.Fatalf("MokList attributes %#x, want NV|BS", v.Attr)
		}
		list, err := hex.DecodeString(v.Data)
		if err != nil {
			t.Fatal(err)
		}
		var certs []string
		for len(list) >= 28 {
			size := int(list[16]) | int(list[17])<<8 | int(list[18])<<16 | int(list[19])<<24
			if size < 44 || size > len(list) {
				t.Fatalf("malformed MokList")
			}
			if bytes.Equal(list[28:44], owner) {
				certs = append(certs, hex.EncodeToString(list[44:size]))
			}
			list = list[size:]
		}
		return certs
	}
	return nil
}

func startSWTPM(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "sock")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "swtpm", "socket", "--tpmstate", "dir="+dir,
		"--ctrl", "type=unixio,path="+sock, "--tpm2", "--flags", "startup-clear")
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start swtpm: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(sock); err == nil {
			return sock
		}
		if time.Now().After(deadline) {
			t.Fatal("swtpm socket did not appear")
		}
	}
}

func modCheckReport(t *testing.T, phase string, out []byte) string {
	t.Helper()
	s := strings.ReplaceAll(string(out), "\r", "")
	begin := strings.LastIndex(s, modCheckBegin)
	if begin < 0 {
		t.Fatalf("%s: the installed OS did not run the check. tail:\n%s", phase, tail(out, 6000))
	}
	end := strings.Index(s[begin:], modCheckEnd)
	if end < 0 {
		t.Fatalf("%s: check output is incomplete:\n%s", phase, s[begin:])
	}
	report := s[begin : begin+end]
	t.Logf("%s report:\n%s", phase, report)
	return report
}

func requireLine(t *testing.T, report, prefix string) {
	t.Helper()
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(prefix)).MatchString(report) {
		t.Fatalf("report has no line %q:\n%s", prefix, report)
	}
}

func field(report, key string) string {
	m := regexp.MustCompile(`(?m)^` + key + `=(\S*)`).FindStringSubmatch(report)
	if m == nil {
		return ""
	}
	return m[1]
}
