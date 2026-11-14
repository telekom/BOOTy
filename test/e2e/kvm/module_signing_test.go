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
grep -i 'BOOTy module signing' /proc/keys | sed 's/^/KEY /'
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

// TestModuleSigningSecureBootVM provisions an Ubuntu cloud image with
// MODULE_SIGNING_ENABLED=true under OVMF with the Microsoft keys enrolled
// and Secure Boot off, lets mok-enroll.efi write MokList on the next boot,
// turns Secure Boot on (as a BMC would) and boots the installed system
// through shim. The BOOTy signed module must load; an unsigned module and
// one signed by another host's key must be refused. With Secure Boot off
// again the system must still boot. A second provisioning must replace the
// certificate in MokList instead of adding one.
func TestModuleSigningSecureBootVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	osImage := os.Getenv("MIRROR_OS_IMAGE")
	helper := os.Getenv("BOOTY_MOK_ENROLL_EFI")
	code := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd")
	varsTemplate := envOrDefault("OVMF_VARS_MS", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd")
	for _, f := range []string{osImage, helper, code, varsTemplate} {
		if _, err := os.Stat(f); f == "" || err != nil {
			failOrSkipUnsupportedHost(t, "module signing asset missing (MIRROR_OS_IMAGE, BOOTY_MOK_ENROLL_EFI, OVMF): %q %v", f, err)
		}
	}
	for _, tool := range []string{"virt-fw-vars", "swtpm", "zstd"} {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}

	dir := t.TempDir()
	gzImage, _ := prepareModuleImage(t, osImage, dir)
	baseURL := startImageServer(t, gzImage)
	helperEFI, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	initramfs := buildProvisionInitramfsWithFiles(t, map[string]string{
		"HOSTNAME":                  "modsign-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     baseURL + "/image.gz",
		"MODE":                      "provision",
		"MIN_DISK_SIZE_GB":          "1",
		"MODULE_SIGNING_ENABLED":    "true",
	}, map[string]string{"usr/lib/booty/mok-enroll.efi": string(helperEFI)})
	kernel := findKernel(t)

	vars := filepath.Join(dir, "OVMF_VARS.fd")
	copyBinary(t, varsTemplate, vars)
	setSecureBoot(t, vars, false)
	disk := filepath.Join(dir, "disk.qcow2")
	run(t, "create disk", "qemu-img", "create", "-f", "qcow2", disk, "6G")
	// swtpm exits when QEMU disconnects, so every boot gets a new one.
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
			"-drive", "file=" + disk + ",format=qcow2,if=virtio",
			"-chardev", "socket,id=chrtpm,path=" + tpm,
			"-tpmdev", "emulator,id=tpm0,chardev=chrtpm", "-device", "tpm-tis,tpmdev=tpm0",
		}, extra...)
	}
	provision := func(phase string) {
		out := runMirrorQEMU(t, phase, vm("-kernel", kernel, "-initrd", initramfs,
			"-append", runnerKernelCmdline+" net.ifnames=0", "-net", "nic,model=virtio", "-net", "user"), 10*time.Minute)
		if !strings.Contains(string(out), "step=report-success") || strings.Contains(string(out), "provisioning step failed") {
			t.Fatalf("%s did not succeed", phase)
		}
	}
	enroll := func(phase string) string {
		out := runMirrorQEMU(t, phase, vm("-net", "none"), 3*time.Minute)
		if !strings.Contains(string(out), "BOOTy mok-enroll: MokList updated") {
			t.Fatalf("%s: mok-enroll.efi did not run:\n%s", phase, tail(out, 4000))
		}
		return string(out)
	}

	// Provisioning with Secure Boot off, then the BootNext boot of the helper.
	provision("provision")
	enroll("mok-enroll")
	certs := bootyMOKCerts(t, vars)
	if len(certs) != 1 {
		t.Fatalf("MokList holds %d BOOTy certificates, want 1", len(certs))
	}

	// Secure Boot on: shim mirrors MokList, the kernel trusts the key.
	setSecureBoot(t, vars, true)
	report := modCheckReport(t, "secure boot on", runMirrorQEMU(t, "secure boot on", vm("-net", "none"), 8*time.Minute))
	requireLine(t, report, "SECUREBOOT=1")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "SIGNED_SIGNER=BOOTy module signing modsign-e2e")
	requireLine(t, report, "UNSIGNED_MODULE=refused")
	requireLine(t, report, "OTHERKEY_MODULE=refused")
	pcr7On := field(report, "PCR7")

	// Secure Boot off: the system still boots, unsigned modules load.
	setSecureBoot(t, vars, false)
	report = modCheckReport(t, "secure boot off", runMirrorQEMU(t, "secure boot off", vm("-net", "none"), 8*time.Minute))
	requireLine(t, report, "SECUREBOOT=0")
	requireLine(t, report, "SIGNED_MODULE=loaded")
	requireLine(t, report, "UNSIGNED_MODULE=loaded")
	if pcr7On == "" || pcr7On == field(report, "PCR7") {
		t.Fatalf("PCR 7 did not change with the Secure Boot state: %q", pcr7On)
	}

	// Reprovisioning replaces the old certificate.
	provision("reprovision")
	enroll("mok-enroll after reprovision")
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
	report = modCheckReport(t, "db only", runMirrorQEMU(t, "db only", vm("-net", "none"), 8*time.Minute))
	requireLine(t, report, "SECUREBOOT=1")
	requireLine(t, report, "UNSIGNED_MODULE=refused")
	if field(report, "SIGNED_MODULE") == "loaded" {
		t.Log("db only: the kernel accepts modules signed by a db certificate (distribution patch)")
	} else {
		t.Log("db only: the kernel refuses modules signed by a db certificate (upstream behaviour)")
	}
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
