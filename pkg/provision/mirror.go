//go:build linux

package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/blockdev"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

// Root disk mirroring follows the CAPRF legacy provisioner: the image is
// written to the first disk, its root partition becomes the only member of a
// degraded RAID1 array with end-of-device metadata, the partition table and
// EFI system partition are copied to the second disk, and the second root
// partition joins the array and resyncs in the background.

var (
	copyBlockDeviceFn           = disk.CopyBlockDevice
	checkMirrorTailSpaceFn      = disk.CheckMirrorTailSpace
	shrinkFilesystemForMirrorFn = (*disk.Manager).ShrinkFilesystemForMirror
)

func (o *Orchestrator) mirroringMode() string {
	return strings.ToLower(strings.TrimSpace(o.cfg.Provision.Disk.MirroringMode))
}

// validateMirroringMode rejects mirroring setups that the mirror-root step
// cannot handle. It runs in validate-provision-inputs, before setup-raid and
// detect-disk change the disks or the disk selection.
func (o *Orchestrator) validateMirroringMode() error {
	mode := o.mirroringMode()
	switch mode {
	case "", disk.MirroringModeNone:
		return nil
	case disk.MirroringModeRAID1, disk.MirroringModeAuto:
	default:
		return fmt.Errorf("unsupported disk mirroring mode %q", mode)
	}
	imageMode := strings.ToLower(strings.TrimSpace(o.cfg.Provision.Image.Mode))
	if len(o.cfg.Provision.Disk.RAID) > 0 || o.cfg.Provision.Disk.PartitionLayout != nil ||
		strings.TrimSpace(o.cfg.Provision.Disk.NVMeNamespaces) != "" ||
		(imageMode != "" && imageMode != config.ImageModeWholeDisk) {
		return fmt.Errorf("disk mirroring mode %q requires a whole-disk image and cannot be combined with explicit RAID, NVMe namespace setup or a partition layout", mode)
	}
	return nil
}

// selectMirrorPair sets targetDisk and mirrorDisk when mirroring is requested
// and returns true when it chose the target disk. It returns false to fall
// back to single-disk detection.
func (o *Orchestrator) selectMirrorPair(ctx context.Context) (bool, error) {
	o.mirrorDisk = ""
	mode := o.mirroringMode()
	switch mode {
	case "", disk.MirroringModeNone:
		return false, nil
	case disk.MirroringModeRAID1, disk.MirroringModeAuto:
	default:
		return false, fmt.Errorf("unsupported disk mirroring mode %q", mode)
	}
	if o.resumeMirrorPair[0] != "" {
		return o.useResumeMirrorPair()
	}
	first, second, ok, err := o.disk.DetectMirrorPair(ctx, o.cfg.Provision.Disk.MinSizeGB)
	if err != nil {
		return false, err
	}
	if !ok {
		if mode == disk.MirroringModeRAID1 {
			return false, fmt.Errorf("disk mirroring mode raid-1 requested but no pair of equally sized disks was found")
		}
		o.log.Info("no matching disk pair, provisioning a single disk", "mirroringMode", mode)
		return false, nil
	}
	o.targetDisk = first
	o.mirrorDisk = second
	o.log.Info("mirroring root disk", "mirroringMode", mode, "disk1", first, "disk2", second)
	return true, nil
}

// useResumeMirrorPair keeps the disk selection made before a resume: the
// mirror pair, or the single disk that auto mode fell back to. Once disks are
// written, a new selection could mount a raw array member or an unwritten
// disk, so a missing disk is an error.
func (o *Orchestrator) useResumeMirrorPair() (bool, error) {
	for _, dev := range o.resumeMirrorPair {
		if dev == "" {
			continue
		}
		if _, err := statPath(dev); err != nil {
			return false, fmt.Errorf("disk %s selected before resume is unavailable: %w", dev, err)
		}
	}
	o.targetDisk, o.mirrorDisk = o.resumeMirrorPair[0], o.resumeMirrorPair[1]
	o.log.Info("using the disk selection made before resume", "disk1", o.targetDisk, "disk2", o.mirrorDisk)
	return true, nil
}

// mirrorRoot turns the root partition of the streamed image, and a separate
// /boot partition if the image has one, into RAID1 arrays across both disks.
// It is idempotent so it can rerun on resume.
func (o *Orchestrator) mirrorRoot(ctx context.Context) error {
	if o.mirrorDisk == "" {
		return nil
	}
	rootNum, err := disk.PartitionNumberChecked(o.rootPartition, o.targetDisk)
	if err != nil {
		return fmt.Errorf("determine root partition number: %w", err)
	}
	bootData, err := o.separateBootPartition()
	if err != nil {
		return err
	}

	o.mirrorArrays = []string{disk.MirrorRootArray}
	rootPrepare := func() error {
		// Grow before creating the array: metadata 1.0 lives at the end of
		// the member, past the end of the filesystem the image carried.
		if err := o.disk.GrowPartition(ctx, o.targetDisk, rootNum); err != nil {
			return err
		}
		if err := checkMirrorTailSpaceFn(o.rootPartition); err != nil {
			return err
		}
		return o.disk.ReplicatePartitionTable(ctx, o.targetDisk, o.mirrorDisk)
	}
	if err := o.mirrorPartition(ctx, disk.MirrorRootArray, o.rootPartition, rootPrepare); err != nil {
		return err
	}
	o.rootPartition = disk.MirrorRootArray
	if bootData == "" {
		return nil
	}

	// A separate /boot sits before other partitions and cannot grow, so its
	// filesystem shrinks to make room for the metadata.
	o.mirrorArrays = append(o.mirrorArrays, disk.MirrorBootArray)
	bootPrepare := func() error {
		if err := shrinkFilesystemForMirrorFn(o.disk, ctx, bootData); err != nil {
			return err
		}
		return checkMirrorTailSpaceFn(bootData)
	}
	return o.mirrorPartition(ctx, disk.MirrorBootArray, bootData, bootPrepare)
}

// mirrorPartition makes member the first device of array and adds the
// partition with the same number on the mirror disk. prepare runs before the
// array is created. An already assembled array only gets a missing second
// member, so a resume after a failed mdadm --add completes the mirror.
func (o *Orchestrator) mirrorPartition(ctx context.Context, array, member string, prepare func() error) error {
	num, err := disk.PartitionNumberChecked(member, o.targetDisk)
	if err != nil {
		return fmt.Errorf("determine partition number of %s: %w", member, err)
	}
	mirrorMember := blockdev.PartitionDevicePath(o.mirrorDisk, num)
	if o.disk.MirrorActive(ctx, array) {
		o.log.Info("mirror already assembled", "array", array)
		present, err := o.disk.MirrorHasMember(ctx, array, mirrorMember)
		if err != nil || present {
			return err
		}
		return o.disk.AddMirrorMember(ctx, array, mirrorMember)
	}
	if err := prepare(); err != nil {
		return err
	}
	// The replicated partition can still carry an md superblock or
	// filesystem signature from an earlier install of the second disk.
	if err := o.disk.WipeFilesystemSignatures(ctx, mirrorMember); err != nil {
		return err
	}
	if err := o.disk.CreateDegradedMirror(ctx, array, member); err != nil {
		return err
	}
	return o.disk.AddMirrorMember(ctx, array, mirrorMember)
}

// GPT partition types that mirroring handles besides root and the ESP.
const (
	// biosBootPartitionGUID is the BIOS boot partition that GRUB embeds its
	// core image into.
	biosBootPartitionGUID = "21686148-6449-6E6F-744E-656564454649"
	// xbootldrPartitionGUID is the extended boot partition that images such
	// as the Ubuntu 24.04 cloud image use for a separate /boot.
	xbootldrPartitionGUID = "BC13C2FF-59E6-4262-A352-B275FD6F7172"
)

// separateBootPartition returns the image partition, besides root, ESP and
// BIOS boot, that mirroring also covers: one Linux filesystem or extended
// boot partition, the separate /boot that the legacy provisioner mirrors as
// md1. Any other partition is rejected because its data would exist on the
// first disk only.
func (o *Orchestrator) separateBootPartition() (string, error) {
	var boot string
	for _, p := range o.targetPartitions {
		switch {
		case p.Node == o.rootPartition || p.Node == o.bootPartition ||
			strings.EqualFold(p.Type, disk.EFISystemPartitionGUID) || strings.EqualFold(p.Type, biosBootPartitionGUID):
			continue
		case boot == "" && (strings.EqualFold(p.Type, disk.LinuxFilesystemGUID) || strings.EqualFold(p.Type, xbootldrPartitionGUID)):
			boot = p.Node
		default:
			return "", fmt.Errorf("disk mirroring covers the root partition, one separate /boot partition, the ESP and "+
				"the BIOS boot partition, but the image also has partition %s (type %s)", p.Node, p.Type)
		}
	}
	return boot, nil
}

// mdadmConfHeader matches the CAPRF legacy provisioner. AUTO stops mdadm
// from assembling arrays that are not listed, such as foreign arrays on
// other disks, at boot.
const mdadmConfHeader = "# mdadm.conf - written by BOOTy\n\nDEVICE partitions\nHOMEHOST <system>\nMAILADDR root\n\n" +
	"AUTO +imsm +1.x -all\n\n"

// mdadmArrayLines returns the ARRAY lines of mdadm --detail --scan output with
// the device named array. mdadm reports /dev/md/<name>; the legacy
// provisioner records /dev/mdN. Assembly matches arrays by UUID.
func mdadmArrayLines(array, scan string) []string {
	var lines []string
	for line := range strings.Lines(scan) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "ARRAY" {
			continue
		}
		fields[1] = array
		lines = append(lines, strings.Join(fields, " "))
	}
	return lines
}

// writeMdadmConf writes /etc/mdadm/mdadm.conf, which Debian-based images
// read, and also /etc/mdadm.conf when the image ships one, because mdadm and
// dracut use that file when it exists.
func writeMdadmConf(root, content string) error {
	paths := []string{filepath.Join(root, "etc", "mdadm", "mdadm.conf")}
	if alt := filepath.Join(root, "etc", "mdadm.conf"); fileExists(alt) {
		paths = append(paths, alt)
	}
	for _, path := range paths {
		if err := writeTargetRootFile(root, path, []byte(content)); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

const mirrorGrubConfig = "# Written by BOOTy: the root filesystem is a RAID1 array. Booting by the\n" +
	"# PARTUUID of one member would bypass the array.\nGRUB_FORCE_PARTUUID=\n"

// configureMdadm records the root array in the installed system and rebuilds
// its initramfs so the array is assembled before the root filesystem mounts.
func (o *Orchestrator) configureMdadm(ctx context.Context) error {
	if o.mirrorDisk == "" {
		return nil
	}
	var arrayLines []string
	for _, array := range o.mirrorArrays {
		scan, err := o.disk.MirrorScan(ctx, array)
		if err != nil {
			return err
		}
		arrayLines = append(arrayLines, mdadmArrayLines(array, scan)...)
	}
	if err := writeMdadmConf(newroot, mdadmConfHeader+strings.Join(arrayLines, "\n")+"\n"); err != nil {
		return err
	}
	// Ubuntu cloud images set GRUB_FORCE_PARTUUID, which boots the kernel
	// without initramfs from one member partition and bypasses the array.
	// configure-grub runs update-grub after this step, so clear it here.
	if err := writeTargetRootFile(newroot, filepath.Join(newroot, "etc", "default", "grub.d", "99-booty-mirror.cfg"),
		[]byte(mirrorGrubConfig)); err != nil {
		return fmt.Errorf("write grub mirror config: %w", err)
	}
	// Without mdadm in the target, update-initramfs still succeeds but the
	// initramfs cannot assemble the root array.
	cmd := "if ! command -v mdadm >/dev/null 2>&1; then " +
		"echo 'mdadm is not installed in the target image' >&2; exit 1; fi; " +
		"if command -v update-initramfs >/dev/null 2>&1; then update-initramfs -u -k all; " +
		"else dracut --force --regenerate-all; fi"
	if out, err := o.disk.ChrootRun(ctx, newroot, cmd); err != nil {
		return fmt.Errorf("rebuild initramfs for root mirror: %s: %w", string(out), err)
	}
	return nil
}

// createMirrorEFIBootEntry makes the second disk bootable on its own, so the
// host still boots when the first disk fails. On UEFI it adds a firmware boot
// entry for the second disk; on BIOS it installs GRUB into both disks.
// Both copy the ESP, if the image has one, after the last step that writes
// to it.
func (o *Orchestrator) createMirrorEFIBootEntry(ctx context.Context) error {
	if o.mirrorDisk == "" {
		return nil
	}
	if ok, reason := efiRuntimeReady(); !ok {
		o.log.Info("no UEFI runtime, installing the BIOS bootloader on the mirror disk", "reason", reason, "disk", o.mirrorDisk)
		if err := o.installMirrorBIOSBootloader(ctx); err != nil {
			return err
		}
		if o.bootPartition == "" {
			return nil
		}
		mirrorEFI, err := o.mirrorESP()
		if err != nil {
			return err
		}
		return o.syncMirrorESP(ctx, mirrorEFI)
	}
	if o.bootPartition == "" {
		return fmt.Errorf("the image has no EFI system partition, so the mirror disk %s cannot boot on UEFI", o.mirrorDisk)
	}
	mirrorEFI, err := o.mirrorESP()
	if err != nil {
		return err
	}
	// Copy first: a retry after a failed copy then starts over with the
	// copy, and the entry is only created for a complete ESP. The loader path
	// comes from the ESP, which syncMirrorESP mounts again.
	if err := o.syncMirrorESP(ctx, mirrorEFI); err != nil {
		return err
	}
	return o.config.CreateEFIBootEntry(ctx, o.mirrorDisk, mirrorEFI)
}

// mirrorESP returns the ESP partition of the mirror disk.
func (o *Orchestrator) mirrorESP() (string, error) {
	efiNum, err := disk.PartitionNumberChecked(o.bootPartition, o.targetDisk)
	if err != nil {
		return "", fmt.Errorf("determine EFI partition number: %w", err)
	}
	return blockdev.PartitionDevicePath(o.mirrorDisk, efiNum), nil
}

// installMirrorBIOSBootloader installs GRUB for BIOS boot into both disks
// from the target root, like the legacy provisioner. The replicated partition
// table gives the second disk the BIOS boot partition that GRUB embeds its
// core image into.
func (o *Orchestrator) installMirrorBIOSBootloader(ctx context.Context) error {
	for _, d := range []string{o.targetDisk, o.mirrorDisk} {
		dev, err := validateChrootDevicePath("mirror disk", d)
		if err != nil {
			return err
		}
		if out, err := o.disk.ChrootRun(ctx, newroot, "grub-install --target=i386-pc "+dev); err != nil {
			return fmt.Errorf("install BIOS bootloader on %s: %s: %w", dev, string(out), err)
		}
	}
	return nil
}

// syncMirrorESP copies the ESP block device with the ESP unmounted, so the
// copy is consistent, and mounts it again for the remaining steps.
func (o *Orchestrator) syncMirrorESP(ctx context.Context, mirrorEFI string) error {
	if err := o.unmountBoot(); err != nil {
		return err
	}
	if err := copyBlockDeviceFn(o.bootPartition, mirrorEFI); err != nil {
		return fmt.Errorf("copy EFI system partition: %w", err)
	}
	return o.mountBoot(ctx)
}

// writeTargetRootFile writes data to path below root without following
// symlinks out of the target root, which comes from the streamed image.
func writeTargetRootFile(root, path string, data []byte) error {
	if err := ensureWithinRoot(root, path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := ensureTargetParentWithinRoot(root, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := ensureTargetParentWithinRoot(root, dir); err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o644)
}
