//go:build e2e

package kvm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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
	// luksCAPath is CA_BUNDLE_FILE in the provisioning ramdisk.
	luksCAPath = "deploy/ca-bundle.pem"
)

// luksCheckScript runs in the installed OS on every boot, after the TPM2
// enrollment unit, and reports the state of the volume and of GRUB.
const luksCheckScript = `#!/bin/sh
exec >/dev/ttyS0 2>&1
echo "` + luksCheckBegin + `"
echo "SECUREBOOT=$(od -An -tu1 -j4 -N1 /sys/firmware/efi/efivars/SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c | tr -d ' ')"
echo "PCR7=$(cat /sys/class/tpm/tpm0/pcr-sha256/7 2>/dev/null)"
echo "PCR14=$(cat /sys/class/tpm/tpm0/pcr-sha256/14 2>/dev/null)"
echo "ROOT_TYPE=$(lsblk -no TYPE "$(findmnt -no SOURCE /)")"
echo "ROOT_VG=$(lvs --noheadings -o vg_name "$(findmnt -no SOURCE /)" 2>/dev/null | tr -d ' ')"
echo "ROOT_PV=$(pvs --noheadings -o pv_name 2>/dev/null | tr -d ' ' | tr '\n' ',')"
echo "CMDLINE_LVM=$(grep -o 'rd.luks.uuid=[^ ]* rd.lvm.vg=[^ ]*' /proc/cmdline)"
echo "GRUB_LOCK=$(grep -c 'superusers="booty-locked"' /boot/grub/grub.cfg)"
echo "GRUB_UNRESTRICTED=$(grep -c 'menuentry_id_option="--unrestricted' /boot/grub/grub.cfg)"
echo "GRUB_RECOVERY=$(grep -c 'recovery mode' /boot/grub/grub.cfg)"
echo "BOOTY_FILES=$(ls /etc/booty/luks-tpm2.env /etc/grub.d/01_booty_luks_lock /etc/dracut.conf.d/90-booty-luks.conf 2>/dev/null | wc -l)"
echo "BOOTORDER=$(efibootmgr 2>/dev/null | sed -n 's/^BootOrder: //p')"
if [ -d /data ]; then
	echo "DATA_TYPE=$(lsblk -no TYPE "$(findmnt -no SOURCE /data)")"
	echo "DATA_BYTES=$(df -B1 --output=size /data | tail -n 1 | tr -d ' ')"
	echo "DATA_MARKER=$(cat /data/marker 2>/dev/null)"
fi
if [ -f /etc/booty/luks-tpm2.env ]; then
	. /etc/booty/luks-tpm2.env
	dev=/dev/disk/by-uuid/$LUKS_UUID
	echo "KEYSLOTS=$(cryptsetup luksDump "$dev" | sed -n '/^Keyslots:/,/^Tokens:/p' | sed -n 's/^  \([0-9]*\): luks2.*/\1/p' | tr '\n' ',')"
	echo "TPM2_TOKENS=$(cryptsetup luksDump "$dev" | grep -c 'systemd-tpm2')"
	echo "ENROLL_KEY=$(test -e /etc/booty/luks-enroll.key && echo present || echo absent)"
	echo "BOUND=$(test -e /etc/booty/luks-tpm2.bound && echo yes || echo no)"
	echo "UNIT=$(systemctl is-enabled booty-luks-tpm2-enroll.service 2>&1)"
	echo "INITRD_KEY=$(lsinitrd /boot/initrd.img-$(uname -r) 2>/dev/null | grep -c luks-enroll.key)"
	echo "CRYPTTAB=$(grep "^$LUKS_NAME " /etc/crypttab)"
	journalctl -b -o cat -u booty-luks-tpm2-enroll.service --no-pager | tail -n 30 | sed 's/^/ENROLL /'
fi
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

// DHCP on the first NIC, so the enrollment unit reaches the status URL.
const luksNetplan = `network:
  version: 2
  ethernets:
    e2e:
      match: {name: "e*"}
      dhcp4: true
`

type luksVM struct {
	t       *testing.T
	dir     string
	disk    string
	vars    string
	code    string
	kernel  string
	osRaw   string
	baseURL string
	boots   int
}

// luksLayout changes the cloud image: luksData appends an ext4 partition 2
// (the last one) mounted at /data by PARTUUID; luksLVM moves the root into
// LV root of VG tcaas on partition 1.
type luksLayout int

const (
	luksPlain luksLayout = iota
	luksData
	luksLVM
)

func newLUKSVM(t *testing.T) *luksVM {
	return newLUKSVMWith(t, luksPlain)
}

func newLUKSVMWith(t *testing.T, layout luksLayout) *luksVM {
	t.Helper()
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
	tools := []string{"virt-fw-vars", "swtpm", "cryptsetup", "sfdisk"}
	if layout == luksLVM {
		tools = append(tools, "lvm")
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}
	v := &luksVM{t: t, dir: t.TempDir(), code: code, kernel: findKernel(t)}
	gz := prepareLUKSImage(t, osImage, v.dir, strings.Fields(os.Getenv("DISK_ENCRYPTION_DEBS")), layout)
	v.osRaw = filepath.Join(v.dir, "os.part.json")
	v.baseURL = startImageServer(t, gz)
	v.vars = filepath.Join(v.dir, "OVMF_VARS.fd")
	copyBinary(t, varsTemplate, v.vars)
	setLUKSSecureBoot(t, v.vars, false)
	v.disk = filepath.Join(v.dir, "disk.qcow2")
	size := "6G"
	if layout == luksData {
		// The image is 6 GiB then; the data partition grows into the rest.
		size = "9G"
	}
	run(t, "create disk", "qemu-img", "create", "-f", "qcow2", v.disk, size)
	return v
}

// args returns the QEMU arguments of one boot. One TPM state lives for the
// whole life of the machine; swtpm exits when QEMU disconnects, so every
// boot starts a new process on it.
func (v *luksVM) args(extra ...string) []string {
	v.boots++
	sock := startLUKSSWTPM(v.t, filepath.Join(v.dir, "tpm"), filepath.Join(v.dir, fmt.Sprintf("tpm%d.sock", v.boots)))
	return append([]string{
		"-machine", "q35,smm=on,accel=kvm:tcg", "-cpu", mirrorQEMUCPUModel(), "-m", "2048", "-smp", "2",
		"-nographic", "-no-reboot",
		"-global", "driver=cfi.pflash01,property=secure,value=on",
		"-drive", "if=pflash,format=raw,unit=0,readonly=on,file=" + v.code,
		"-drive", "if=pflash,format=raw,unit=1,file=" + v.vars,
		"-drive", "file=" + v.disk + ",format=qcow2,if=virtio",
		"-chardev", "socket,id=chrtpm,path=" + sock,
		"-tpmdev", "emulator,id=tpm0,chardev=chrtpm", "-device", "tpm-tis,tpmdev=tpm0",
		"-net", "nic,model=virtio", "-net", "user",
	}, extra...)
}

func (v *luksVM) provision(vars, files map[string]string) []byte {
	initramfs := buildProvisionInitramfsWithFiles(v.t, vars, files)
	// loglevel=4: kernel info lines (EXT4 mounts) on the serial console can
	// split a BOOTy line that the tests look for.
	out := runMirrorQEMU(v.t, "provision", v.args("-kernel", v.kernel, "-initrd", initramfs,
		"-append", runnerKernelCmdline+" net.ifnames=0 loglevel=4"), 15*time.Minute)
	if !strings.Contains(string(out), "step=report-success") || strings.Contains(string(out), "provisioning step failed") {
		v.t.Fatal("provisioning did not succeed")
	}
	v.t.Logf("PROVISION_SECONDS=%.1f (report-init to report-success)", provisionSeconds(out))
	return out
}

// provisionSeconds is the time from report-init to report-success in the
// BOOTy log.
func provisionSeconds(out []byte) float64 {
	stamp := func(step string) time.Time {
		m := regexp.MustCompile(`time=(\S+) level=INFO msg="provisioning step" component=provision step=` + step + `\b`).FindSubmatch(out)
		if m == nil {
			return time.Time{}
		}
		ts, _ := time.Parse(time.RFC3339Nano, string(m[1]))
		return ts
	}
	return stamp("report-success").Sub(stamp("report-init")).Seconds()
}

// TestDiskEncryptionTPM2VM provisions an Ubuntu cloud image with
// DISK_ENCRYPTION=luks2-tpm2 under OVMF with the Microsoft keys and Secure
// Boot off, then walks the enrollment: enrollment key while Secure Boot is
// off, binding (phase 1) and key slot removal after a TPM2 unlock (phase 2)
// with Secure Boot on, and the recovery key after a PCR 7 change.
//
//nolint:funlen // one VM life cycle, phase by phase
func TestDiskEncryptionTPM2VM(t *testing.T) {
	v := newLUKSVM(t)
	escrow := startEscrowServer(t)
	var logs [][]byte

	// 1. Provision with Secure Boot off.
	out := v.provision(map[string]string{
		"HOSTNAME":                         "luks-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP":        "true",
		"IMAGE":                            v.baseURL + "/image.gz",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"TOKEN":                            "e2e-token",
		"CA_BUNDLE_FILE":                   "/" + luksCAPath,
		"DISK_ENCRYPTION":                  "luks2-tpm2",
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrow.url + "/recovery-key",
		"DISK_ENCRYPTION_STATUS_URL":       escrow.url + "/status",
	}, map[string]string{luksCAPath: escrow.caPEM})
	logs = append(logs, out)
	if bytes.Contains(out, []byte("encrypting partition in place")) || !bytes.Contains(out, []byte("partition formatted with LUKS2")) {
		t.Fatal("BOOTy did not format the partition before writing it")
	}
	key := escrow.recoveryKey(t)
	enrollKey := readEnrollKey(t, v.disk, key)

	// 2. Secure Boot off: the enrollment key unlocks, the unit waits.
	out = runMirrorQEMU(t, "secure boot off", v.args(), 8*time.Minute)
	logs = append(logs, out)
	report := luksCheckReport(t, "secure boot off", out)
	for _, l := range []string{"SECUREBOOT=0", "ROOT_TYPE=crypt", "TPM2_TOKENS=0", "ENROLL_KEY=present", "KEYSLOTS=0,1,", "BOUND=no", "UNIT=enabled"} {
		luksRequireLine(t, report, l)
	}

	// 3. Secure Boot on, phase 1: bound, slot 1 and the key stay.
	setLUKSSecureBoot(t, v.vars, true)
	out = runMirrorQEMU(t, "phase 1", v.args(), 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "phase 1", out)
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "ENROLL_KEY=present", "BOUND=yes", "UNIT=enabled", "INITRD_KEY=0"} {
		luksRequireLine(t, report, l)
	}
	if !strings.HasPrefix(luksField(report, "KEYSLOTS"), "0,1,") || !strings.Contains(report, "none luks,x-initrd.attach,tpm2-device=auto") {
		t.Fatalf("phase 1 state: key slots %q", luksField(report, "KEYSLOTS"))
	}
	escrow.requireStatus(t, "tpm2-bound")

	// 4. Secure Boot on, phase 2: the TPM2 unlocks without a prompt, then
	// slot 1 and the key go. A prompt is answered only to read the report.
	out, prompted := runQEMUAnswerPrompt(t, "phase 2", v.args(), luksPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "phase 2", out)
	if prompted {
		t.Fatal("the TPM2 did not unlock: passphrase prompt with Secure Boot on")
	}
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "ENROLL_KEY=absent", "BOUND=no", "UNIT=disabled", "INITRD_KEY=0",
		"GRUB_LOCK=1", "GRUB_UNRESTRICTED=1", "GRUB_RECOVERY=0"} {
		luksRequireLine(t, report, l)
	}
	if slots := luksField(report, "KEYSLOTS"); !strings.HasPrefix(slots, "0,") || strings.HasPrefix(slots, "0,1,") {
		t.Fatalf("key slots %q: want the recovery slot 0 and the TPM2 slot, not slot 1", slots)
	}
	escrow.requireStatus(t, "slot1-removed")
	pcr7 := luksField(report, "PCR7")

	// 5. The enrollment key opens nothing any more; copies on the disk are
	// harmless.
	checkEnrollKeyDead(t, v.disk, enrollKey)

	// 6. Secure Boot off: PCR 7 differs, the TPM2 refuses, the recovery key
	// unlocks at the prompt.
	setLUKSSecureBoot(t, v.vars, false)
	out, prompted = runQEMUAnswerPrompt(t, "recovery key", v.args(), luksPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	if !prompted {
		t.Fatal("no passphrase prompt after the PCR 7 change")
	}
	report = luksCheckReport(t, "recovery key", out)
	luksRequireLine(t, report, "SECUREBOOT=0")
	luksRequireLine(t, report, "ROOT_TYPE=crypt")
	if pcr7 == "" || pcr7 == luksField(report, "PCR7") {
		t.Fatalf("PCR 7 did not change with the Secure Boot state: %q", pcr7)
	}

	// 7. The recovery key is on no console.
	for i, l := range logs {
		if bytes.Contains(l, key) || bytes.Contains(l, bytes.ReplaceAll(key, []byte("-"), nil)) {
			t.Fatalf("the recovery key is in the console log of boot %d", i+1)
		}
	}
}

// TestDiskEncryptionOffVM provisions the same image without the disk
// encryption keys: no encryption step runs, the partition table is the one
// of the image with the root grown, the root is plain, and BOOTy writes
// none of its encryption files.
func TestDiskEncryptionOffVM(t *testing.T) {
	v := newLUKSVM(t)
	out := v.provision(map[string]string{
		"HOSTNAME":                  "plain-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     v.baseURL + "/image.gz",
		"MODE":                      "provision",
		"MIN_DISK_SIZE_GB":          "1",
	}, nil)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "level=") {
			continue // not a BOOTy log line
		}
		for _, s := range []string{"prepare-disk-encryption", "close-encrypted-disk", "LUKS2", "recovery key", "disk encryption"} {
			if strings.Contains(line, s) {
				t.Fatalf("BOOTy log mentions %q without DISK_ENCRYPTION: %s", s, line)
			}
		}
	}
	if !bytes.Contains(out, []byte("step=create-efi-boot-entry")) {
		t.Fatal("create-efi-boot-entry did not run")
	}
	// kexec boots the installed system in the same QEMU run; a firmware
	// reboot ends it.
	if !bytes.Contains(out, []byte(luksCheckBegin)) {
		out = runMirrorQEMU(t, "first boot", v.args(), 8*time.Minute)
	}
	report := luksCheckReport(t, "first boot", out)
	for _, l := range []string{"ROOT_TYPE=part", "BOOTY_FILES=0", "GRUB_LOCK=0"} {
		luksRequireLine(t, report, l)
	}
	if luksField(report, "BOOTORDER") == "" {
		t.Fatal("no BootOrder")
	}
	comparePartitionTables(t, v.osRaw, v.disk)
}

// TestDiskEncryptionTPM2DataPartitionVM encrypts a data partition by number
// (the last partition, mounted by PARTUUID) and leaves the root plain: the
// file system grows inside the mapping, fstab mounts it through the
// mapping, the enrollment key unlocks it until the binding.
func TestDiskEncryptionTPM2DataPartitionVM(t *testing.T) {
	v := newLUKSVMWith(t, luksData)
	escrow := startEscrowServer(t)
	v.provision(map[string]string{
		"HOSTNAME":                         "luks-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP":        "true",
		"IMAGE":                            v.baseURL + "/image.gz",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"TOKEN":                            "e2e-token",
		"CA_BUNDLE_FILE":                   "/" + luksCAPath,
		"DISK_ENCRYPTION":                  "luks2-tpm2",
		"DISK_ENCRYPTION_TARGET":           "2",
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrow.url + "/recovery-key",
	}, map[string]string{luksCAPath: escrow.caPEM})
	escrow.recoveryKey(t)
	out := runMirrorQEMU(t, "data partition", v.args(), 8*time.Minute)
	report := luksCheckReport(t, "data partition", out)
	for _, l := range []string{"ROOT_TYPE=part", "DATA_TYPE=crypt", "DATA_MARKER=booty-data", "TPM2_TOKENS=0", "ENROLL_KEY=present", "KEYSLOTS=0,1,"} {
		luksRequireLine(t, report, l)
	}
	// 512 MiB in the image; grown to the disk end (about 3 GiB) inside the mapping.
	var size int64
	if _, err := fmt.Sscan(luksField(report, "DATA_BYTES"), &size); err != nil || size < 1<<30 {
		t.Fatalf("data file system %d bytes, want it grown past 1 GiB (%v)", size, err)
	}
}

// TestDiskEncryptionLVMTPM2VM provisions an Ubuntu 26.04 image whose root is
// LV root of VG tcaas (LVM-on-LUKS): BOOTy encrypts the physical volume, the
// initramfs opens it and activates the VG, and the two-phase TPM2 enrollment
// works as for a root partition. No LVM label reaches the disk in plain text.
//
//nolint:funlen // one VM life cycle, phase by phase
func TestDiskEncryptionLVMTPM2VM(t *testing.T) {
	v := newLUKSVMWith(t, luksLVM)
	escrow := startEscrowServer(t)
	logs := make([][]byte, 0, 4)

	// 1. Provision with Secure Boot off.
	out := v.provision(map[string]string{
		"HOSTNAME":                         "luks-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP":        "true",
		"IMAGE":                            v.baseURL + "/image.gz",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"TOKEN":                            "e2e-token",
		"CA_BUNDLE_FILE":                   "/" + luksCAPath,
		"DISK_ENCRYPTION":                  "luks2-tpm2",
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrow.url + "/recovery-key",
		"DISK_ENCRYPTION_STATUS_URL":       escrow.url + "/status",
	}, map[string]string{luksCAPath: escrow.caPEM})
	logs = append(logs, out)
	if !bytes.Contains(out, []byte("LVM physical volume opened in the LUKS2 mapping")) {
		t.Fatal("BOOTy did not open VG tcaas in the LUKS2 mapping")
	}
	key := escrow.recoveryKey(t)
	checkLVMOnLUKSDisk(t, v.disk, key)

	// 2. Secure Boot off: the enrollment key unlocks, the initramfs
	// activates VG tcaas on the mapping, the unit waits.
	out = runMirrorQEMU(t, "secure boot off", v.args(), 8*time.Minute)
	logs = append(logs, out)
	report := luksCheckReport(t, "secure boot off", out)
	for _, l := range []string{"SECUREBOOT=0", "ROOT_TYPE=lvm", "ROOT_VG=tcaas", "ROOT_PV=/dev/mapper/booty_crypt,", "CMDLINE_LVM=rd.luks.uuid=",
		"TPM2_TOKENS=0", "ENROLL_KEY=present", "KEYSLOTS=0,1,", "BOUND=no", "UNIT=enabled"} {
		luksRequireLine(t, report, l)
	}
	if !strings.Contains(report, " rd.lvm.vg=tcaas") {
		t.Fatal("the kernel command line does not name VG tcaas")
	}

	// 3. Secure Boot on, phase 1: bound to PCR 7+14.
	setLUKSSecureBoot(t, v.vars, true)
	out = runMirrorQEMU(t, "phase 1", v.args(), 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "phase 1", out)
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=lvm", "TPM2_TOKENS=1", "ENROLL_KEY=present", "BOUND=yes", "INITRD_KEY=0"} {
		luksRequireLine(t, report, l)
	}
	escrow.requireStatus(t, "tpm2-bound")

	// 4. Secure Boot on, phase 2: the TPM2 unlocks without a prompt, then
	// slot 1 and the key go.
	out, prompted := runQEMUAnswerPrompt(t, "phase 2", v.args(), luksPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "phase 2", out)
	if prompted {
		t.Fatal("the TPM2 did not unlock: passphrase prompt with Secure Boot on")
	}
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=lvm", "ROOT_VG=tcaas", "TPM2_TOKENS=1", "ENROLL_KEY=absent", "BOUND=no", "UNIT=disabled", "GRUB_LOCK=1", "GRUB_RECOVERY=0"} {
		luksRequireLine(t, report, l)
	}
	if slots := luksField(report, "KEYSLOTS"); !strings.HasPrefix(slots, "0,") || strings.HasPrefix(slots, "0,1,") {
		t.Fatalf("key slots %q: want the recovery slot 0 and the TPM2 slot, not slot 1", slots)
	}
	escrow.requireStatus(t, "slot1-removed")

	// 5. The recovery key is on no console.
	for i, l := range logs {
		if bytes.Contains(l, key) || bytes.Contains(l, bytes.ReplaceAll(key, []byte("-"), nil)) {
			t.Fatalf("the recovery key is in the console log of boot %d", i+1)
		}
	}
}

// convertRootToLVM moves the root file system of the cloud image (partition
// 1, the last one) into LV root of VG tcaas: partition 1 gets 1 GiB more and
// the Linux LVM type, becomes the physical volume, and the file system is
// copied into the LV and grown, which leaves dracut room.
func convertRootToLVM(t *testing.T, raw, dir string) {
	t.Helper()
	rootFS := filepath.Join(dir, "root.ext4")
	loop := strings.TrimSpace(string(runOutput(t, "attach image", "losetup", "-P", "--show", "-f", raw)))
	run(t, "save image root", "dd", "if="+loop+"p1", "of="+rootFS, "bs=4M", "conv=sparse", "status=none")
	run(t, "detach image", "losetup", "-d", loop)
	run(t, "extend image", "truncate", "-s", "+1G", raw)
	run(t, "move backup GPT", "sgdisk", "-e", raw)
	grow := exec.CommandContext(t.Context(), "sfdisk", "--no-reread", "--no-tell-kernel", "-N", "1", raw)
	grow.Stdin = strings.NewReader(", +\n")
	if out, err := grow.CombinedOutput(); err != nil {
		t.Fatalf("grow partition 1: %v\n%s", err, out)
	}
	run(t, "LVM partition type", "sfdisk", "--part-type", raw, "1", "E6D6D379-F507-44C2-A23C-238F2A3DF928")
	// Not a root label: BOOTy must pick the single LVM partition.
	run(t, "LVM partition name", "sfdisk", "--part-label", raw, "1", "tcaas-pv")
	loop = strings.TrimSpace(string(runOutput(t, "attach image", "losetup", "-P", "--show", "-f", raw)))
	defer func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() }()
	// Only the PV of the fixture: a VG tcaas of the host is never touched.
	pv := loop + "p1"
	if _, err := os.Stat("/dev/mapper/tcaas-root"); err == nil {
		t.Fatal("the host has /dev/mapper/tcaas-root; the fixture LV would conflict")
	}
	run(t, "create PV", "lvm", "pvcreate", "--devices", pv, "-ff", "-y", pv)
	defer func() { _ = exec.CommandContext(context.Background(), "lvm", "vgchange", "--devices", pv, "-an", "tcaas").Run() }()
	run(t, "create VG tcaas", "lvm", "vgcreate", "--devices", pv, "tcaas", pv)
	run(t, "create LV root", "lvm", "lvcreate", "--devices", pv, "-y", "-l", "100%FREE", "-n", "root", "tcaas")
	// Not sparse: the physical volume holds the old root file system.
	run(t, "copy root into LV", "dd", "if="+rootFS, "of=/dev/mapper/tcaas-root", "bs=4M", "conv=fsync", "status=none")
	run(t, "check LV root", "e2fsck", "-f", "-p", "/dev/mapper/tcaas-root")
	run(t, "grow LV root", "resize2fs", "/dev/mapper/tcaas-root")
	run(t, "deactivate VG tcaas", "lvm", "vgchange", "--devices", pv, "-an", "tcaas")
	run(t, "detach image", "losetup", "-d", loop)
	if err := os.Remove(rootFS); err != nil {
		t.Fatal(err)
	}
	// The label scan of checkLVMOnLUKSDisk finds a plain physical volume.
	if n := countPVLabels(t, raw); n != 1 {
		t.Fatalf("LVM labels in the image: %d, want 1", n)
	}
}

// checkLVMOnLUKSDisk checks that the escrowed recovery key opens the LUKS2
// partition and that no LVM physical volume label is on the disk in plain
// text: BOOTy wrote the volume only into the mapping.
func checkLVMOnLUKSDisk(t *testing.T, qcow2 string, recovery []byte) {
	t.Helper()
	raw, loop := attachDisk(t, qcow2)
	part := luksPartition(t, loop)
	open := exec.CommandContext(t.Context(), "cryptsetup", "open", "--test-passphrase", "--key-file", "-", part)
	open.Stdin = bytes.NewReader(recovery)
	if out, err := open.CombinedOutput(); err != nil {
		t.Fatalf("the escrowed recovery key does not open %s: %v %s", part, err, out)
	}
	if n := countPVLabels(t, raw); n != 0 {
		t.Fatalf("%d LVM physical volume labels on the disk in plain text", n)
	}
}

// countPVLabels counts sectors with an LVM2 physical volume label.
func countPVLabels(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 4<<20)
	sector := make([]byte, 512)
	n := 0
	for {
		if _, err := io.ReadFull(r, sector); err != nil {
			return n
		}
		if bytes.HasPrefix(sector, []byte("LABELONE")) && bytes.Equal(sector[24:32], []byte("LVM2 001")) {
			n++
		}
	}
}

// addDataPartition grows the image by 2.5 GiB: 2 GiB more for the root
// (partition 1, last in Ubuntu cloud images) and a 512 MiB partition 2
// behind it. sfdisk keeps the partition UUIDs of the dump.
func addDataPartition(t *testing.T, raw string) {
	t.Helper()
	run(t, "extend image", "truncate", "-s", "+2560M", raw)
	run(t, "move backup GPT", "sgdisk", "-e", raw)
	var dump strings.Builder
	for line := range strings.Lines(string(runOutput(t, "dump partition table", "sfdisk", "--dump", raw))) {
		if strings.HasPrefix(line, "last-lba:") {
			continue
		}
		if f := strings.Fields(line); len(f) > 0 && strings.HasSuffix(f[0], "1") && strings.Contains(line, "size=") {
			re := regexp.MustCompile(`size=\s*(\d+)`)
			var size int64
			fmt.Sscan(re.FindStringSubmatch(line)[1], &size)
			line = re.ReplaceAllString(line, fmt.Sprintf("size=%d", size+2*1024*1024*2))
		}
		dump.WriteString(line)
	}
	dump.WriteString(raw + "2 : type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"booty-data\"\n")
	cmd := exec.CommandContext(t.Context(), "sfdisk", "--no-reread", "--no-tell-kernel", raw)
	cmd.Stdin = strings.NewReader(dump.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sfdisk: %v\n%s\n%s", err, out, dump.String())
	}
}

// prepareLUKSImage installs debs (DISK_ENCRYPTION_DEBS, for example
// libtss2-rc0t64 on Ubuntu 26.04), the check service and a DHCP netplan
// into the root file system of the cloud image, saves the partition table
// of the image as os.part.json and returns the gzipped raw image.
func prepareLUKSImage(t *testing.T, osImage, dir string, debs []string, layout luksLayout) string {
	t.Helper()
	dataPartition := layout == luksData
	raw := filepath.Join(dir, "os.raw")
	run(t, "convert OS image to raw", "qemu-img", "convert", "-O", "raw", osImage, raw)
	if dataPartition {
		addDataPartition(t, raw)
	}
	if layout == luksLVM {
		convertRootToLVM(t, raw, dir)
	}
	if err := os.WriteFile(filepath.Join(dir, "os.part.json"), runOutput(t, "image partition table", "sfdisk", "-J", raw), 0o600); err != nil {
		t.Fatal(err)
	}
	loop := strings.TrimSpace(string(runOutput(t, "attach image", "losetup", "-P", "--show", "-f", raw)))
	mnt := filepath.Join(dir, "mnt")
	// Best effort on failure; the success path below unmounts and detaches.
	defer func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() }()
	defer func() { _ = exec.CommandContext(context.Background(), "umount", mnt).Run() }()
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	if dataPartition {
		// The root does not grow when the data partition is last: give it
		// the room that grow-partition would.
		run(t, "check image root", "e2fsck", "-f", "-p", loop+"p1")
		run(t, "grow image root", "resize2fs", loop+"p1")
		run(t, "format data partition", "mkfs.ext4", "-q", "-L", "bootydata", loop+"p2")
		data := filepath.Join(dir, "data")
		if err := os.Mkdir(data, 0o755); err != nil {
			t.Fatal(err)
		}
		run(t, "mount data partition", "mount", loop+"p2", data)
		luksWriteImageFile(t, data, "marker", []byte("booty-data\n"), 0o644)
		run(t, "unmount data partition", "umount", data)
	}
	rootDev := loop + "p1"
	if layout == luksLVM {
		run(t, "activate VG tcaas", "lvm", "vgchange", "--devices", loop+"p1", "-ay", "tcaas")
		defer func() {
			_ = exec.CommandContext(context.Background(), "umount", mnt).Run()
			_ = exec.CommandContext(context.Background(), "lvm", "vgchange", "--devices", loop+"p1", "-an", "tcaas").Run()
		}()
		rootDev = "/dev/mapper/tcaas-root"
	}
	run(t, "mount image root", "mount", rootDev, mnt)
	if dataPartition {
		partUUID := strings.TrimSpace(string(runOutput(t, "data PARTUUID", "blkid", "-o", "value", "-s", "PARTUUID", loop+"p2")))
		fstab, err := os.ReadFile(filepath.Join(mnt, "etc/fstab"))
		if err != nil {
			t.Fatal(err)
		}
		// By PARTUUID: BOOTy must point it at the mapping.
		fstab = append(fstab, []byte("PARTUUID="+partUUID+" /data ext4 defaults 0 2\n")...)
		luksWriteImageFile(t, mnt, "etc/fstab", fstab, 0o644)
		if err := os.MkdirAll(filepath.Join(mnt, "data"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, deb := range debs {
		data, err := os.ReadFile(deb)
		if err != nil {
			t.Fatal(err)
		}
		rel := "tmp/" + filepath.Base(deb)
		luksWriteImageFile(t, mnt, rel, data, 0o644)
		run(t, "install "+filepath.Base(deb), "chroot", mnt, "dpkg", "-i", "/"+rel)
		if err := os.Remove(filepath.Join(mnt, rel)); err != nil {
			t.Fatal(err)
		}
	}
	luksWriteImageFile(t, mnt, "usr/local/lib/booty-luks-check.sh", []byte(luksCheckScript), 0o755)
	luksWriteImageFile(t, mnt, "etc/systemd/system/booty-luks-check.service", []byte(luksCheckUnit), 0o644)
	luksWriteImageFile(t, mnt, "etc/netplan/99-booty-e2e.yaml", []byte(luksNetplan), 0o600)
	link := filepath.Join(mnt, "etc/systemd/system/multi-user.target.wants/booty-luks-check.service")
	if err := os.Symlink("/etc/systemd/system/booty-luks-check.service", link); err != nil {
		t.Fatal(err)
	}
	run(t, "unmount image root", "umount", mnt)
	if layout == luksLVM {
		run(t, "deactivate VG tcaas", "lvm", "vgchange", "--devices", loop+"p1", "-an", "tcaas")
	}
	run(t, "detach image", "losetup", "-d", loop)
	gz := compressGzip(t, raw)
	if err := os.Remove(raw); err != nil {
		t.Fatal(err)
	}
	return gz
}

func luksWriteImageFile(t *testing.T, root, rel string, data []byte, mode os.FileMode) {
	t.Helper()
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		t.Fatal(err)
	}
}

// attachDisk converts the qcow2 disk to a raw file and attaches it.
func attachDisk(t *testing.T, qcow2 string) (raw, loop string) {
	t.Helper()
	raw = filepath.Join(t.TempDir(), "disk.raw")
	run(t, "convert disk to raw", "qemu-img", "convert", "-O", "raw", qcow2, raw)
	loop = strings.TrimSpace(string(runOutput(t, "attach disk", "losetup", "-P", "--show", "-f", raw)))
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "losetup", "-d", loop).Run() })
	return raw, loop
}

func luksPartition(t *testing.T, loop string) string {
	t.Helper()
	parts, _ := filepath.Glob(loop + "p*")
	for _, p := range parts {
		if out, err := exec.CommandContext(t.Context(), "blkid", "-o", "value", "-s", "TYPE", p).Output(); err == nil && strings.TrimSpace(string(out)) == "crypto_LUKS" {
			return p
		}
	}
	t.Fatalf("no LUKS partition on %s", loop)
	return ""
}

// readEnrollKey opens the volume with the recovery key on the host and
// reads the enrollment key that BOOTy wrote into it.
func readEnrollKey(t *testing.T, qcow2 string, recovery []byte) []byte {
	t.Helper()
	raw, loop := attachDisk(t, qcow2)
	part := luksPartition(t, loop)
	open := exec.CommandContext(t.Context(), "cryptsetup", "open", "--readonly", "--key-file", "-", part, "booty_e2e")
	open.Stdin = bytes.NewReader(recovery)
	if out, err := open.CombinedOutput(); err != nil {
		t.Fatalf("the escrowed recovery key does not open %s: %v %s", part, err, out)
	}
	defer func() { _ = exec.CommandContext(context.Background(), "cryptsetup", "close", "booty_e2e").Run() }()
	mnt := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "mount encrypted root", "mount", "-o", "ro,noload", "/dev/mapper/booty_e2e", mnt)
	defer func() { _ = exec.CommandContext(context.Background(), "umount", mnt).Run() }()
	key, err := os.ReadFile(filepath.Join(mnt, "etc/booty/luks-enroll.key"))
	if err != nil || len(key) != 64 {
		t.Fatalf("enrollment key: %d bytes, %v", len(key), err)
	}
	_ = raw
	return key
}

// checkEnrollKeyDead counts copies of the enrollment key left on the disk
// (old initramfs blocks on /boot) and checks that it opens no key slot.
func checkEnrollKeyDead(t *testing.T, qcow2 string, enrollKey []byte) {
	t.Helper()
	raw, loop := attachDisk(t, qcow2)
	t.Logf("enrollment key copies left on the disk: %d (they open nothing, checked below)", countInFile(t, raw, enrollKey))
	keyFile := filepath.Join(t.TempDir(), "enroll.key")
	if err := os.WriteFile(keyFile, enrollKey, 0o600); err != nil {
		t.Fatal(err)
	}
	part := luksPartition(t, loop)
	if out, err := exec.CommandContext(t.Context(), "cryptsetup", "open", "--test-passphrase", "--key-file", keyFile, part).CombinedOutput(); err == nil {
		t.Fatalf("the enrollment key still opens %s: %s", part, out)
	}
}

func countInFile(t *testing.T, path string, needle []byte) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 4<<20)
	buf := make([]byte, 4<<20+len(needle))
	keep, n := 0, 0
	for {
		m, err := io.ReadFull(r, buf[keep:4<<20+keep])
		chunk := buf[:keep+m]
		n += bytes.Count(chunk, needle)
		if err != nil {
			return n
		}
		keep = copy(buf, chunk[len(chunk)-len(needle)+1:])
	}
}

// comparePartitionTables checks that the provisioned disk has the
// partitions of the image (start, type, UUID, name), the root at least as
// large as in the image.
func comparePartitionTables(t *testing.T, imageJSON, qcow2 string) {
	t.Helper()
	type table struct {
		PartitionTable struct {
			Partitions []struct {
				Node  string `json:"node"`
				Start int64  `json:"start"`
				Size  int64  `json:"size"`
				Type  string `json:"type"`
				UUID  string `json:"uuid"`
				Name  string `json:"name"`
			} `json:"partitions"`
		} `json:"partitiontable"`
	}
	var want, got table
	data, err := os.ReadFile(imageJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	raw, _ := attachDisk(t, qcow2)
	if err := json.Unmarshal(runOutput(t, "disk partition table", "sfdisk", "-J", raw), &got); err != nil {
		t.Fatal(err)
	}
	w, g := want.PartitionTable.Partitions, got.PartitionTable.Partitions
	if len(w) != len(g) {
		t.Fatalf("disk has %d partitions, the image %d", len(g), len(w))
	}
	for i := range w {
		if w[i].Start != g[i].Start || w[i].Type != g[i].Type || w[i].UUID != g[i].UUID || w[i].Name != g[i].Name || g[i].Size < w[i].Size {
			t.Fatalf("partition %d differs from the image: %+v, image %+v", i+1, g[i], w[i])
		}
	}
}

type escrowServer struct {
	url      string
	caPEM    string
	mu       sync.Mutex
	keys     [][]byte
	statuses []string
}

// startEscrowServer stands in for the provisioner endpoints of
// DISK_ENCRYPTION_RECOVERY_KEY_URL and DISK_ENCRYPTION_STATUS_URL: HTTPS
// on the QEMU host address with a test CA, bearer token required.
func startEscrowServer(t *testing.T) *escrowServer {
	t.Helper()
	s := &escrowServer{}
	cert, caPEM := luksTestCert(t)
	s.caPEM = caPEM
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer e2e-token" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			switch r.URL.Path {
			case "/recovery-key":
				s.keys = append(s.keys, body)
			case "/status":
				var st struct{ Status, Message string }
				_ = json.Unmarshal(body, &st)
				t.Logf("status callback: %s %s", st.Status, st.Message)
				s.statuses = append(s.statuses, st.Status)
			}
			w.WriteHeader(http.StatusNoContent)
		})}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	s.url = fmt.Sprintf("https://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port)
	return s
}

// luksTestCert returns a server certificate for 10.0.2.2 and its CA (PEM).
func luksTestCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "BOOTy e2e CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "10.0.2.2"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("10.0.2.2")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
}

func (s *escrowServer) recoveryKey(t *testing.T) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.keys) != 1 {
		t.Fatalf("recovery key callbacks: %d, want 1", len(s.keys))
	}
	var body struct {
		Hostname    string `json:"hostname"`
		LUKSUUID    string `json:"luksUUID"`
		KeySlot     int    `json:"keySlot"`
		RecoveryKey string `json:"recoveryKey"`
	}
	if err := json.Unmarshal(s.keys[0], &body); err != nil {
		t.Fatalf("recovery key callback is not JSON: %v", err)
	}
	if body.Hostname != "luks-e2e" || body.LUKSUUID == "" || body.KeySlot != 0 || len(body.RecoveryKey) != 71 {
		t.Fatalf("recovery key callback: hostname %q uuid %q slot %d key length %d", body.Hostname, body.LUKSUUID, body.KeySlot, len(body.RecoveryKey))
	}
	return []byte(body.RecoveryKey)
}

func (s *escrowServer) requireStatus(t *testing.T, status string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.statuses {
		if st == status {
			return
		}
	}
	t.Fatalf("no %q status callback, got %v", status, s.statuses)
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
// console once prompt appears. It reports whether the prompt appeared.
func runQEMUAnswerPrompt(t *testing.T, phase string, args []string, prompt string, answer []byte, timeout time.Duration) ([]byte, bool) {
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
	cancel() // stops the prompt watcher if the prompt never came
	return []byte(out.String()), <-answered
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
