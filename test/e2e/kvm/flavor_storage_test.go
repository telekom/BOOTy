//go:build e2e

package kvm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFlavorInitrdStorageControllersQEMU boots a release initramfs flavor
// (initrd.Dockerfile) with the Debian kernel it ships modules for. The
// target disk is NVMe and a second disk sits on an AHCI controller; there is
// no virtio disk, and the firmware is OVMF. The Debian kernel builds nvme,
// ahci, md and efivarfs as modules, so without them in the initramfs BOOTy
// finds no disk. The run also needs working busybox applets (blockdev, sync).
//
// BOOTY_FLAVOR_INITRAMFS: initramfs.cpio.zst of a flavor build.
// BOOTY_FLAVOR_KERNEL: vmlinuz of `--target kernel-image`.
func TestFlavorInitrdStorageControllersQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	flavor := os.Getenv("BOOTY_FLAVOR_INITRAMFS")
	kernel := os.Getenv("BOOTY_FLAVOR_KERNEL")
	if flavor == "" || kernel == "" {
		failOrSkipUnsupportedHost(t, "BOOTY_FLAVOR_INITRAMFS and BOOTY_FLAVOR_KERNEL are required")
	}
	requireKVMAssets(t, flavor, kernel)

	rawDisk := createChrootCapableTestDiskImage(t, 512)
	// create-efi-boot-entry registers \EFI\ubuntu\shimx64.efi.
	writeImageESPFile(t, rawDisk, "EFI/ubuntu/shimx64.efi", "BOOTy KVM e2e shim\n")
	caprf := startFakeCAPRF(t, compressGzip(t, rawDisk))

	dir := t.TempDir()
	nvmeDisk := filepath.Join(dir, "nvme.raw")
	sataDisk := filepath.Join(dir, "sata.raw")
	run(t, "create NVMe disk", "truncate", "-s", "2G", nvmeDisk)
	run(t, "create SATA disk", "truncate", "-s", "1G", sataDisk)
	// A partition table on the SATA disk proves the wipe of every disk reached it.
	sgdisk := exec.Command("sgdisk", "-n", "1:0:+64M", sataDisk)
	if out, err := sgdisk.CombinedOutput(); err != nil {
		t.Fatalf("partition SATA disk: %v\n%s", err, out)
	}

	vars := caprf.nativeVars("provision")
	vars["PROVISION_TARGET_OS"] = "linux"
	vars["IMAGE"] = caprf.baseURL + "/image.gz"
	vars["IMAGE_ALLOW_INSECURE_HTTP"] = "true"
	vars["DISK_DEVICE"] = "/dev/nvme0n1"
	vars["WIPE_ALL_DISKS"] = "true"
	vars["SUCCESS_ACTION"] = "poweroff"
	initramfs := flavorInitramfsWithVars(t, flavor, vars)

	// UEFI, so efivarfs loads (it fails with ENODEV on BIOS).
	ovmfCode := envOrDefault("OVMF_CODE", findOVMF("/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_CODE.fd"))
	ovmfVars := filepath.Join(dir, "OVMF_VARS.fd")
	copyFile(t, findOVMF("/usr/share/OVMF/OVMF_VARS_4M.fd", "/usr/share/OVMF/OVMF_VARS.fd"), ovmfVars)

	args := []string{
		"-m", "2048", "-nographic", "-no-reboot",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + ovmfCode,
		"-drive", "if=pflash,format=raw,file=" + ovmfVars,
		"-kernel", kernel,
		"-initrd", initramfs,
		"-drive", "file=" + nvmeDisk + ",format=raw,if=none,id=nv0",
		"-device", "nvme,serial=booty-nvme,drive=nv0",
		"-device", "ahci,id=ahci0",
		"-drive", "file=" + sataDisk + ",format=raw,if=none,id=sata0",
		"-device", "ide-hd,drive=sata0,bus=ahci0.0",
		"-net", "nic,model=virtio,macaddr=52:54:00:12:34:56",
		"-net", "user",
		"-append", "console=ttyS0 panic=1 net.ifnames=0",
	}
	args = append(args, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "qemu-system-x86_64", args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("flavor QEMU run timed out. tail:\n%s", tail(out, 4000))
	}
	t.Logf("flavor QEMU run exited (%v). tail:\n%s", err, tail(out, 3000))

	for _, ko := range []string{"nvme.ko", "ahci.ko", "md-mod.ko", "raid1.ko", "efivarfs.ko"} {
		if !strings.Contains(string(out), `msg="loaded kernel module" module=`+ko) {
			t.Errorf("kernel module %s was not loaded", ko)
		}
	}
	assertProvisionSucceeded(t, out)
	caprf.requireStatuses(t, "init", "success")
	requirePowerOff(t, out)

	// The image went to the NVMe disk.
	if table := runOutput(t, "read NVMe partition table", "sfdisk", "-d", nvmeDisk); !bytes.Contains(table, []byte("C12A7328-F81F-11D2-BA4B-00A0C93EC93B")) {
		t.Fatalf("NVMe disk has no image partition table:\n%s", table)
	}
	// The AHCI disk was wiped by WIPE_ALL_DISKS.
	if out, err := exec.Command("sfdisk", "-d", sataDisk).CombinedOutput(); err == nil {
		t.Fatalf("SATA disk still has a partition table after the wipe of every disk:\n%s", out)
	}
}

// flavorInitramfsWithVars appends /deploy/vars to a flavor initramfs. The
// kernel unpacks concatenated archives in order, each with its own
// compression.
func flavorInitramfsWithVars(t *testing.T, flavor string, vars map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeDeployVars(t, dir, vars)
	out := filepath.Join(t.TempDir(), "flavor-with-vars.cpio")
	copyFile(t, flavor, out)
	cmd := exec.Command("sh", "-c", `find deploy -print0 | cpio --null -o --format=newc 2>/dev/null | gzip >> "$1"`, "sh", out)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("append vars to flavor initramfs: %v\n%s", err, output)
	}
	return out
}

// writeImageESPFile writes a file into the ESP (partition 1) of a raw image.
func writeImageESPFile(t *testing.T, rawImage, rel, content string) {
	t.Helper()
	loopDev := strings.TrimSpace(string(runOutput(t, "setup ESP loop device",
		"losetup", "--find", "--show", "--partscan", rawImage)))
	defer func() { _ = exec.Command("losetup", "-d", loopDev).Run() }()
	waitForDevice(t, loopDev+"p1", 5*time.Second)
	mnt := filepath.Join(t.TempDir(), "esp")
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	mountWithRetry(t, "mount image ESP", loopDev+"p1", mnt)
	defer func() { _ = exec.Command("umount", mnt).Run() }()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(mnt, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mnt, rel), content)
	run(t, "unmount image ESP", "umount", mnt)
}
