//go:build e2e

package kvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	mirrorCheckBegin = "BOOTY-MIRROR-CHECK-BEGIN"
	mirrorCheckEnd   = "BOOTY-MIRROR-CHECK-END"
)

// mirrorCheckScript runs in the provisioned OS on every boot. It waits for a
// running resync, prints the root device, the RAID state and the firmware
// boot entries to the serial console, then powers the VM off.
const mirrorCheckScript = `#!/bin/sh
exec >/dev/ttyS0 2>&1
echo "` + mirrorCheckBegin + `"
for md in /dev/md[0-9]* /dev/md/*; do
	[ -b "$md" ] && mdadm --wait "$md" >/dev/null 2>&1
done
echo "ROOT_SOURCE=$(findmnt -n -o SOURCE /)"
echo "ESP_PARTUUID=$(findmnt -n -o PARTUUID /boot/efi)"
cat /proc/mdstat
efibootmgr -v
echo "` + mirrorCheckEnd + `"
systemctl poweroff --no-block
`

const mirrorCheckUnit = `[Unit]
Description=BOOTy mirrored root e2e check
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/bin/sh /usr/local/lib/booty-mirror-check.sh

[Install]
WantedBy=multi-user.target
`

// TestMirroredRootBootFailoverVM installs a real Ubuntu cloud image with
// DISK_MIRRORING_MODE=raid-1 onto two disks under UEFI, boots the installed
// system from both disks, then boots it again with the first disk removed.
// The second boot must come up degraded from the second disk through its own
// firmware boot entry.
func TestMirroredRootBootFailoverVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	osImage := os.Getenv("MIRROR_OS_IMAGE")
	if osImage == "" {
		failOrSkipUnsupportedHost(t, "MIRROR_OS_IMAGE not set: path to an Ubuntu cloud image (qcow2)")
	}
	ovmfCode := envOrDefault("OVMF_CODE", findOVMF("/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_CODE.fd"))
	ovmfVarsTemplate := envOrDefault("OVMF_VARS", findOVMF("/usr/share/OVMF/OVMF_VARS_4M.fd", "/usr/share/OVMF/OVMF_VARS.fd"))
	for _, f := range []string{osImage, ovmfCode, ovmfVarsTemplate} {
		if _, err := os.Stat(f); err != nil {
			failOrSkipUnsupportedHost(t, "mirror boot asset missing: %v", err)
		}
	}

	dir := t.TempDir()
	rawImage := filepath.Join(dir, "os.raw")
	run(t, "convert OS image to raw", "qemu-img", "convert", "-O", "raw", osImage, rawImage)
	gzImage := compressGzip(t, rawImage)
	if err := os.Remove(rawImage); err != nil {
		t.Fatalf("remove raw image: %v", err)
	}
	baseURL := startImageServer(t, gzImage)

	ovmfVars := filepath.Join(dir, "OVMF_VARS.fd")
	copyBinary(t, ovmfVarsTemplate, ovmfVars)
	disk1 := filepath.Join(dir, "disk1.qcow2")
	disk2 := filepath.Join(dir, "disk2.qcow2")
	for _, d := range []string{disk1, disk2} {
		run(t, "create mirror disk", "qemu-img", "create", "-f", "qcow2", d, "4G")
	}

	initramfs := buildProvisionInitramfsWithFiles(t, map[string]string{
		"HOSTNAME":                  "mirror-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     baseURL + "/image.gz",
		"MODE":                      "provision",
		"MIN_DISK_SIZE_GB":          "1",
		"DISK_MIRRORING_MODE":       "raid-1",
		"DISABLE_KEXEC":             "true",
	}, map[string]string{
		"file-system/usr/local/lib/booty-mirror-check.sh":           mirrorCheckScript,
		"file-system/etc/systemd/system/booty-mirror-check.service": mirrorCheckUnit,
		"machine-commands/enable-mirror-check":                      "systemctl enable booty-mirror-check.service",
	})

	kernel := findKernel(t)
	provisionArgs := append(mirrorQEMUBaseArgs(ovmfCode, ovmfVars, disk1, disk2),
		"-kernel", kernel,
		"-initrd", initramfs,
		"-append", "console=ttyS0 panic=1 net.ifnames=0",
		"-net", "nic,model=virtio",
		"-net", "user",
	)
	out := runMirrorQEMU(t, "provision", provisionArgs, 15*time.Minute)
	if !strings.Contains(string(out), "step=report-success") || strings.Contains(string(out), "provisioning step failed") {
		t.Fatalf("mirrored provisioning did not succeed. tail:\n%s", tail(out, 8000))
	}

	// Boot 1: both disks. The check waits for the resync, so the second
	// member is in sync before the first disk is removed.
	report := mirrorCheckReport(t, "boot with both disks",
		runMirrorQEMU(t, "boot with both disks", mirrorQEMUBaseArgs(ovmfCode, ovmfVars, disk1, disk2), 10*time.Minute))
	requireRootOnMD(t, report)
	if !strings.Contains(report, "[2/2] [UU]") {
		t.Fatalf("root array is not a clean two-member mirror:\n%s", report)
	}

	// Boot 2: only the second disk. Firmware must use the second disk's boot
	// entry and the initramfs must start the array degraded.
	report = mirrorCheckReport(t, "boot from second disk only",
		runMirrorQEMU(t, "boot from second disk only", mirrorQEMUBaseArgs(ovmfCode, ovmfVars, disk2), 10*time.Minute))
	requireRootOnMD(t, report)
	if !strings.Contains(report, "[2/1]") {
		t.Fatalf("root array is not degraded to one member:\n%s", report)
	}
	requireBootCurrentOnESP(t, report)
}

// mirrorQEMUBaseArgs returns UEFI QEMU arguments with the given disks.
func mirrorQEMUBaseArgs(ovmfCode, ovmfVars string, disks ...string) []string {
	args := []string{
		"-machine", "q35,accel=kvm:tcg",
		"-cpu", "max",
		"-m", "2048",
		"-smp", "2",
		"-nographic",
		"-no-reboot",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + ovmfCode,
		"-drive", "if=pflash,format=raw,file=" + ovmfVars,
	}
	for _, d := range disks {
		args = append(args, "-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", d))
	}
	return append(args, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)
}

func runMirrorQEMU(t *testing.T, phase string, args []string, timeout time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "qemu-system-x86_64", args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("QEMU %s timed out after %s. output:\n%s", phase, timeout, readableSerial(out))
	}
	t.Logf("QEMU %s exited (%v). output:\n%s", phase, err, readableSerial(out))
	return out
}

// mirrorCheckReport returns the check script output from a boot.
func mirrorCheckReport(t *testing.T, phase string, out []byte) string {
	t.Helper()
	s := strings.ReplaceAll(string(out), "\r", "")
	begin := strings.LastIndex(s, mirrorCheckBegin)
	if begin < 0 {
		t.Fatalf("%s: the installed OS did not run the mirror check. tail:\n%s", phase, tail(out, 6000))
	}
	end := strings.Index(s[begin:], mirrorCheckEnd)
	if end < 0 {
		t.Fatalf("%s: mirror check output is incomplete:\n%s", phase, s[begin:])
	}
	report := s[begin : begin+end]
	t.Logf("%s report:\n%s", phase, report)
	return report
}

func requireRootOnMD(t *testing.T, report string) {
	t.Helper()
	m := regexp.MustCompile(`ROOT_SOURCE=(\S+)`).FindStringSubmatch(report)
	if m == nil || !strings.HasPrefix(m[1], "/dev/md") {
		t.Fatalf("root filesystem is not on the md array:\n%s", report)
	}
}

// requireBootCurrentOnESP checks that firmware booted the entry whose device
// path names the mounted ESP, which is the second disk's ESP in this boot.
func requireBootCurrentOnESP(t *testing.T, report string) {
	t.Helper()
	esp := regexp.MustCompile(`ESP_PARTUUID=(\S+)`).FindStringSubmatch(report)
	current := regexp.MustCompile(`BootCurrent: ([0-9A-Fa-f]{4})`).FindStringSubmatch(report)
	if esp == nil || current == nil {
		t.Fatalf("ESP PARTUUID or BootCurrent missing:\n%s", report)
	}
	entry := regexp.MustCompile(`(?m)^Boot` + current[1] + `\*? .*$`).FindString(report)
	if !strings.Contains(strings.ToLower(entry), strings.ToLower(esp[1])) {
		t.Fatalf("firmware booted Boot%s %q, not the entry for the second disk ESP %s:\n%s",
			current[1], entry, esp[1], report)
	}
}

// readableSerial drops ANSI escapes and systemd progress spinner lines from
// serial output so the full log stays readable in the test output.
func readableSerial(out []byte) string {
	ansi := regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	var b strings.Builder
	for _, line := range strings.Split(ansi.ReplaceAllString(string(out), ""), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.Contains(line, "A start job is running") || strings.TrimSpace(line) == "M" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
