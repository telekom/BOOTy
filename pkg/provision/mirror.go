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
	copyBlockDeviceFn      = disk.CopyBlockDevice
	checkMirrorTailSpaceFn = disk.CheckMirrorTailSpace
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
		(imageMode != "" && imageMode != config.ImageModeWholeDisk) {
		return fmt.Errorf("disk mirroring mode %q requires a whole-disk image and cannot be combined with explicit RAID or a partition layout", mode)
	}
	return nil
}

// selectMirrorPair sets targetDisk and mirrorDisk when mirroring is requested
// and returns true when it chose the pair. It returns false to fall back to
// single-disk detection.
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

// mirrorRoot turns the root partition of the streamed image into a RAID1
// array across both disks. It is idempotent so it can rerun on resume.
func (o *Orchestrator) mirrorRoot(ctx context.Context) error {
	if o.mirrorDisk == "" {
		return nil
	}
	rootNum, err := disk.PartitionNumberChecked(o.rootPartition, o.targetDisk)
	if err != nil {
		return fmt.Errorf("determine root partition number: %w", err)
	}
	mirrorRoot := blockdev.PartitionDevicePath(o.mirrorDisk, rootNum)

	if o.disk.MirrorActive(ctx, disk.MirrorRootArray) {
		// A resume after a failed mdadm --add finds a degraded array, so
		// check the second member instead of trusting the assembled array.
		o.log.Info("root mirror already assembled", "array", disk.MirrorRootArray)
		if err := o.ensureMirrorMember(ctx, mirrorRoot); err != nil {
			return err
		}
		o.rootPartition = disk.MirrorRootArray
		return nil
	}

	// Grow before creating the array: metadata 1.0 lives at the end of the
	// member, past the end of the filesystem the image carried.
	if err := o.disk.GrowPartition(ctx, o.targetDisk, rootNum); err != nil {
		return err
	}
	if err := checkMirrorTailSpaceFn(o.rootPartition); err != nil {
		return err
	}
	if err := o.disk.ReplicatePartitionTable(ctx, o.targetDisk, o.mirrorDisk); err != nil {
		return err
	}
	// The replicated partition can still carry an md superblock or
	// filesystem signature from an earlier install of the second disk.
	if err := o.disk.WipeFilesystemSignatures(ctx, mirrorRoot); err != nil {
		return err
	}
	if err := o.disk.CreateDegradedMirror(ctx, disk.MirrorRootArray, o.rootPartition); err != nil {
		return err
	}
	if err := o.disk.AddMirrorMember(ctx, disk.MirrorRootArray, mirrorRoot); err != nil {
		return err
	}
	o.rootPartition = disk.MirrorRootArray
	return nil
}

func (o *Orchestrator) ensureMirrorMember(ctx context.Context, member string) error {
	present, err := o.disk.MirrorHasMember(ctx, disk.MirrorRootArray, member)
	if err != nil || present {
		return err
	}
	return o.disk.AddMirrorMember(ctx, disk.MirrorRootArray, member)
}

// configureMdadm records the root array in the installed system and rebuilds
// its initramfs so the array is assembled before the root filesystem mounts.
func (o *Orchestrator) configureMdadm(ctx context.Context) error {
	if o.mirrorDisk == "" {
		return nil
	}
	scan, err := o.disk.MirrorScan(ctx, disk.MirrorRootArray)
	if err != nil {
		return err
	}
	conf := "# mdadm.conf - written by BOOTy\n\nDEVICE partitions\nHOMEHOST <system>\nMAILADDR root\n\n" + scan
	if err := writeTargetRootFile(newroot, filepath.Join(newroot, "etc", "mdadm", "mdadm.conf"), []byte(conf)); err != nil {
		return fmt.Errorf("write mdadm.conf: %w", err)
	}
	cmd := "if command -v update-initramfs >/dev/null 2>&1; then update-initramfs -u -k all; " +
		"else dracut --force --regenerate-all; fi"
	if out, err := o.disk.ChrootRun(ctx, newroot, cmd); err != nil {
		return fmt.Errorf("rebuild initramfs for root mirror: %s: %w", string(out), err)
	}
	return nil
}

// createMirrorEFIBootEntry copies the EFI system partition to the second
// disk and adds a firmware boot entry for it, so the host still boots when
// the first disk fails. It runs after the last step that writes to the ESP.
func (o *Orchestrator) createMirrorEFIBootEntry(ctx context.Context) error {
	if o.mirrorDisk == "" || o.bootPartition == "" {
		return nil
	}
	efiNum, err := disk.PartitionNumberChecked(o.bootPartition, o.targetDisk)
	if err != nil {
		return fmt.Errorf("determine EFI partition number: %w", err)
	}
	mirrorEFI := blockdev.PartitionDevicePath(o.mirrorDisk, efiNum)
	// The loader path comes from the mounted ESP, so create the entry first.
	if err := o.config.CreateEFIBootEntry(ctx, o.mirrorDisk, mirrorEFI); err != nil {
		return err
	}
	return o.syncMirrorESP(ctx, mirrorEFI)
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
