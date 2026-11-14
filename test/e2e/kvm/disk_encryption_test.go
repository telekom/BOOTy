//go:build e2e

package kvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	luksCheckBegin = "BOOTY-LUKS-CHECK-BEGIN"
	luksCheckEnd   = "BOOTY-LUKS-CHECK-END"
	// luksPrompt is what systemd-cryptsetup asks when the TPM2 refuses.
	luksPrompt = "passphrase for disk"
)

// luksCheckScript runs in the installed OS on every boot, after the TPM2
// enrollment unit, and reports the volume state.
const luksCheckScript = `#!/bin/sh
exec >/dev/ttyS0 2>&1
. /etc/booty/luks-tpm2.env
dev=/dev/disk/by-uuid/$LUKS_UUID
echo "` + luksCheckBegin + `"
echo "SECUREBOOT=$(od -An -tu1 -j4 -N1 /sys/firmware/efi/efivars/SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c | tr -d ' ')"
echo "PCR7=$(cat /sys/class/tpm/tpm0/pcr-sha256/7 2>/dev/null)"
echo "PCR14=$(cat /sys/class/tpm/tpm0/pcr-sha256/14 2>/dev/null)"
echo "ROOT_TYPE=$(lsblk -no TYPE "$(findmnt -no SOURCE /)")"
echo "KEYSLOTS=$(cryptsetup luksDump "$dev" | sed -n '/^Keyslots:/,/^Tokens:/p' | sed -n 's/^  \([0-9]*\): luks2.*/\1/p' | tr '\n' ',')"
echo "TPM2_TOKENS=$(cryptsetup luksDump "$dev" | grep -c 'systemd-tpm2')"
echo "ENROLL_KEY=$(test -e /etc/booty/luks-enroll.key && echo present || echo absent)"
echo "UNIT=$(systemctl is-enabled booty-luks-tpm2-enroll.service 2>&1)"
echo "INITRD_KEY=$(lsinitrd /boot/initrd.img-$(uname -r) 2>/dev/null | grep -c luks-enroll.key)"
echo "CRYPTTAB=$(grep "^$LUKS_NAME " /etc/crypttab)"
journalctl -b -o cat -u booty-luks-tpm2-enroll.service --no-pager | tail -n 30 | sed 's/^/ENROLL /'
journalctl -b -o cat --no-pager | grep -i -e 'tpm2' -e 'cryptsetup' | tail -n 30 | sed 's/^/JOURNAL /'
echo "` + luksCheckEnd + `"
systemctl poweroff --no-block
`

const luksCheckUnit = `[Unit]
Description=BOOTy disk encryption e2e check
After=local-fs.target booty-luks-tpm2-enroll.service
Before=serial-getty@ttyS0.service

[Service]
Type=oneshot
ExecStart=/bin/sh /usr/local/lib/booty-luks-check.sh

[Install]
WantedBy=multi-user.target
`

// TestDiskEncryptionTPM2VM provisions an Ubuntu cloud image with
// DISK_ENCRYPTION=luks2-tpm2 under OVMF with the Microsoft keys and Secure
// Boot off. The recovery key must reach the callback and no console log.
// With Secure Boot off the installed system unlocks with the enrollment
// key and does not bind; with Secure Boot on it binds to PCR 7+14 and
// removes the enrollment key; the next Secure Boot boot unlocks with the
// TPM2 alone. With Secure Boot off again PCR 7 differs, the TPM2 refuses
// and the recovery key, typed at the prompt, unlocks the root.
//
//nolint:funlen // one VM life cycle, phase by phase
func TestDiskEncryptionTPM2VM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	osImage := os.Getenv("MIRROR_OS_IMAGE")
	code := envOrDefault("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd")
	varsTemplate := envOrDefault("OVMF_VARS_MS", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd")
	for _, f := range []string{osImage, code, varsTemplate} {
		if _, err := os.Stat(f); f == "" || err != nil {
			failOrSkipUnsupportedHost(t, "disk encryption asset missing (MIRROR_OS_IMAGE, OVMF): %q %v", f, err)
		}
	}
	for _, tool := range []string{"virt-fw-vars", "swtpm", "cryptsetup"} {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}

	dir := t.TempDir()
	baseURL := startImageServer(t, prepareLUKSImage(t, osImage, dir, strings.Fields(os.Getenv("DISK_ENCRYPTION_DEBS"))))
	escrow := startEscrowServer(t)
	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                         "luks-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP":        "true",
		"IMAGE":                            baseURL + "/image.gz",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"DISK_ENCRYPTION":                  "luks2-tpm2",
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrow.url,
		// The test server is plain HTTP on the QEMU host address.
		"INSECURE_TRANSPORT": "true",
	})
	kernel := findKernel(t)

	vars := filepath.Join(dir, "OVMF_VARS.fd")
	copyBinary(t, varsTemplate, vars)
	setLUKSSecureBoot(t, vars, false)
	disk := filepath.Join(dir, "disk.qcow2")
	run(t, "create disk", "qemu-img", "create", "-f", "qcow2", disk, "6G")
	// One TPM state for the whole life of the machine; swtpm exits when
	// QEMU disconnects, so every boot starts a new process on it.
	tpmState := filepath.Join(dir, "tpm")
	boots := 0
	vm := func(extra ...string) []string {
		boots++
		sock := startLUKSSWTPM(t, tpmState, filepath.Join(dir, fmt.Sprintf("tpm%d.sock", boots)))
		return append([]string{
			"-machine", "q35,smm=on,accel=kvm:tcg", "-cpu", mirrorQEMUCPUModel(), "-m", "2048", "-smp", "2",
			"-nographic", "-no-reboot",
			"-global", "driver=cfi.pflash01,property=secure,value=on",
			"-drive", "if=pflash,format=raw,unit=0,readonly=on,file=" + code,
			"-drive", "if=pflash,format=raw,unit=1,file=" + vars,
			"-drive", "file=" + disk + ",format=qcow2,if=virtio",
			"-chardev", "socket,id=chrtpm,path=" + sock,
			"-tpmdev", "emulator,id=tpm0,chardev=chrtpm", "-device", "tpm-tis,tpmdev=tpm0",
		}, extra...)
	}
	var logs [][]byte

	// 1. Provision with Secure Boot off.
	out := runMirrorQEMU(t, "provision", vm("-kernel", kernel, "-initrd", initramfs,
		"-append", runnerKernelCmdline+" net.ifnames=0", "-net", "nic,model=virtio", "-net", "user"), 15*time.Minute)
	logs = append(logs, out)
	if !strings.Contains(string(out), "step=report-success") || strings.Contains(string(out), "provisioning step failed") {
		t.Fatal("provisioning did not succeed")
	}
	key := escrow.recoveryKey(t)

	// 2. Secure Boot off: the enrollment key unlocks, nothing is bound.
	out = runMirrorQEMU(t, "secure boot off before enrollment", vm("-net", "none"), 8*time.Minute)
	logs = append(logs, out)
	report := luksCheckReport(t, "secure boot off before enrollment", out)
	luksRequireLine(t, report, "SECUREBOOT=0")
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	luksRequireLine(t, report, "TPM2_TOKENS=0")
	luksRequireLine(t, report, "ENROLL_KEY=present")
	luksRequireLine(t, report, "KEYSLOTS=0,1,")

	// 3. Secure Boot on: the unit binds to PCR 7+14 and removes the key.
	setLUKSSecureBoot(t, vars, true)
	out = runMirrorQEMU(t, "first secure boot", vm("-net", "none"), 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "first secure boot", out)
	luksRequireLine(t, report, "SECUREBOOT=1")
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	luksRequireLine(t, report, "TPM2_TOKENS=1")
	luksRequireLine(t, report, "ENROLL_KEY=absent")
	luksRequireLine(t, report, "UNIT=disabled")
	luksRequireLine(t, report, "INITRD_KEY=0")
	luksRequireLine(t, report, "CRYPTTAB=booty_crypt UUID=")
	if !strings.Contains(luksField(report, "KEYSLOTS"), "0,") || strings.HasPrefix(luksField(report, "KEYSLOTS"), "0,1,") {
		t.Fatalf("key slots %q: want the recovery slot 0 and the TPM2 slot, not the enrollment slot 1", luksField(report, "KEYSLOTS"))
	}
	if !strings.Contains(report, "tpm2-device=auto") {
		t.Fatal("crypttab does not use the TPM2")
	}

	// 4. Secure Boot on again: the TPM2 unlocks without a prompt.
	out = runMirrorQEMU(t, "tpm2 unlock", vm("-net", "none"), 8*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "tpm2 unlock", out)
	luksRequireLine(t, report, "SECUREBOOT=1")
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	if strings.Contains(string(out), luksPrompt) {
		t.Fatal("the TPM2 did not unlock: passphrase prompt with Secure Boot on")
	}
	pcr7 := luksField(report, "PCR7")

	// 5. Secure Boot off: PCR 7 differs, the TPM2 refuses, the recovery key
	// unlocks at the prompt.
	setLUKSSecureBoot(t, vars, false)
	out = runQEMUAnswerPrompt(t, "recovery key", vm("-net", "none"), luksPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "recovery key", out)
	luksRequireLine(t, report, "SECUREBOOT=0")
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	if pcr7 == "" || pcr7 == luksField(report, "PCR7") {
		t.Fatalf("PCR 7 did not change with the Secure Boot state: %q", pcr7)
	}

	// 6. The recovery key is on no console.
	for i, l := range logs {
		if bytes.Contains(l, key) || bytes.Contains(l, bytes.ReplaceAll(key, []byte("-"), nil)) {
			t.Fatalf("the recovery key is in the console log of boot %d", i+1)
		}
	}
}

// prepareLUKSImage installs debs (DISK_ENCRYPTION_DEBS, for example
// libtss2-rc0t64 on Ubuntu 26.04) and the check service into the root file
// system of the cloud image and returns the gzipped raw image.
func prepareLUKSImage(t *testing.T, osImage, dir string, debs []string) string {
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
	for _, deb := range debs {
		data, err := os.ReadFile(deb)
		if err != nil {
			t.Fatal(err)
		}
		rel := "tmp/" + filepath.Base(deb)
		luksWriteImageFile(t, mnt, rel, data)
		run(t, "install "+filepath.Base(deb), "chroot", mnt, "dpkg", "-i", "/"+rel)
		if err := os.Remove(filepath.Join(mnt, rel)); err != nil {
			t.Fatal(err)
		}
	}
	luksWriteImageFile(t, mnt, "usr/local/lib/booty-luks-check.sh", []byte(luksCheckScript))
	luksWriteImageFile(t, mnt, "etc/systemd/system/booty-luks-check.service", []byte(luksCheckUnit))
	link := filepath.Join(mnt, "etc/systemd/system/multi-user.target.wants/booty-luks-check.service")
	if err := os.Symlink("/etc/systemd/system/booty-luks-check.service", link); err != nil {
		t.Fatal(err)
	}
	run(t, "unmount image root", "umount", mnt)
	run(t, "detach image", "losetup", "-d", loop)
	gz := compressGzip(t, raw)
	if err := os.Remove(raw); err != nil {
		t.Fatal(err)
	}
	return gz
}

func luksWriteImageFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type escrowServer struct {
	url    string
	mu     sync.Mutex
	bodies [][]byte
}

// startEscrowServer stands in for the provisioner endpoint of
// DISK_ENCRYPTION_RECOVERY_KEY_URL.
func startEscrowServer(t *testing.T) *escrowServer {
	t.Helper()
	s := &escrowServer{}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	s.url = fmt.Sprintf("http://10.0.2.2:%d/recovery-key", listener.Addr().(*net.TCPAddr).Port)
	return s
}

func (s *escrowServer) recoveryKey(t *testing.T) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) != 1 {
		t.Fatalf("recovery key callbacks: %d, want 1", len(s.bodies))
	}
	var body struct {
		Hostname    string `json:"hostname"`
		LUKSUUID    string `json:"luksUUID"`
		KeySlot     int    `json:"keySlot"`
		RecoveryKey string `json:"recoveryKey"`
	}
	if err := json.Unmarshal(s.bodies[0], &body); err != nil {
		t.Fatalf("recovery key callback is not JSON: %v", err)
	}
	if body.Hostname != "luks-e2e" || body.LUKSUUID == "" || body.KeySlot != 0 || len(body.RecoveryKey) != 71 {
		t.Fatalf("recovery key callback: hostname %q uuid %q slot %d key length %d", body.Hostname, body.LUKSUUID, body.KeySlot, len(body.RecoveryKey))
	}
	return []byte(body.RecoveryKey)
}

// setLUKSSecureBoot sets SecureBootEnable in the variable store, like a BMC
// that turns Secure Boot on or off.
func setLUKSSecureBoot(t *testing.T, vars string, on bool) {
	t.Helper()
	flag := "--set-false"
	if on {
		flag = "--set-true"
	}
	run(t, "set SecureBootEnable", "virt-fw-vars", "--inplace", vars, flag, "SecureBootEnable")
}

// startLUKSSWTPM starts swtpm on the persistent state dir with a new socket.
func startLUKSSWTPM(t *testing.T, state, sock string) string {
	t.Helper()
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "swtpm", "socket", "--tpmstate", "dir="+state,
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

// runQEMUAnswerPrompt is runMirrorQEMU that types answer on the serial
// console once prompt appears.
func runQEMUAnswerPrompt(t *testing.T, phase string, args []string, prompt string, answer []byte, timeout time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out lockedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start QEMU %s: %v", phase, err)
	}
	answered := make(chan bool, 1)
	go func() {
		for ctx.Err() == nil {
			if strings.Contains(out.String(), prompt) {
				time.Sleep(2 * time.Second) // let the prompt switch the terminal mode
				_, werr := stdin.Write(append(append([]byte(nil), answer...), '\r'))
				answered <- werr == nil
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		answered <- false
	}()
	err = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("QEMU %s timed out after %s. output:\n%s", phase, timeout, readableSerial([]byte(out.String())))
	}
	t.Logf("QEMU %s exited (%v). output:\n%s", phase, err, readableSerial([]byte(out.String())))
	if !<-answered {
		t.Fatalf("%s: no %q prompt", phase, prompt)
	}
	return []byte(out.String())
}

func luksCheckReport(t *testing.T, phase string, out []byte) string {
	t.Helper()
	s := strings.ReplaceAll(string(out), "\r", "")
	begin := strings.LastIndex(s, luksCheckBegin)
	if begin < 0 {
		t.Fatalf("%s: the installed OS did not run the check. tail:\n%s", phase, tail(out, 6000))
	}
	end := strings.Index(s[begin:], luksCheckEnd)
	if end < 0 {
		t.Fatalf("%s: check output is incomplete:\n%s", phase, s[begin:])
	}
	report := s[begin : begin+end]
	t.Logf("%s report:\n%s", phase, report)
	return report
}

// luksRequireLine and luksField read the KEY=value lines of a check report.
func luksRequireLine(t *testing.T, report, prefix string) {
	t.Helper()
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(prefix)).MatchString(report) {
		t.Fatalf("report has no line %q:\n%s", prefix, report)
	}
}

func luksField(report, key string) string {
	m := regexp.MustCompile(`(?m)^` + key + `=(\S*)`).FindStringSubmatch(report)
	if m == nil {
		return ""
	}
	return m[1]
}
