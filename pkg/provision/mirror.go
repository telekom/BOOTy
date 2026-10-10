//go:build linux

package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/blockdev"
	"github.com/telekom/BOOTy/pkg/disk"
)

// Root disk mirroring follows the CAPRF legacy provisioner: the image is
// written to the first disk, its root partition becomes the only member of a
// degraded RAID1 array with end-of-device metadata, the partition table and
// EFI system partition are copied to the second disk, and the second root
// partition joins the array and resyncs in the background.

var copyBlockDeviceFn = disk.CopyBlockDevice

func (o *Orchestrator) mirroringMode() string {
	return strings.ToLower(strings.TrimSpace(o.cfg.Provision.Disk.MirroringMode))
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
	if len(o.cfg.Provision.Disk.RAID) > 0 || o.cfg.Provision.Disk.PartitionLayout != nil || o.isABImageMode() {
		return false, fmt.Errorf("disk mirroring mode %q cannot be combined with explicit RAID, partition layout or A/B image mode", mode)
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
		o.log.Info("root mirror already assembled", "array", disk.MirrorRootArray)
		o.rootPartition = disk.MirrorRootArray
		return nil
	}

	// Grow before creating the array: metadata 1.0 lives at the end of the
	// member, past the end of the filesystem the image carried.
	if err := o.disk.GrowPartition(ctx, o.targetDisk, rootNum); err != nil {
		return err
	}
	if err := o.disk.ReplicatePartitionTable(ctx, o.targetDisk, o.mirrorDisk); err != nil {
		return err
	}
	if o.bootPartition != "" {
		efiNum, numErr := disk.PartitionNumberChecked(o.bootPartition, o.targetDisk)
		if numErr != nil {
			return fmt.Errorf("determine EFI partition number: %w", numErr)
		}
		mirrorEFI := blockdev.PartitionDevicePath(o.mirrorDisk, efiNum)
		if err := copyBlockDeviceFn(o.bootPartition, mirrorEFI); err != nil {
			return fmt.Errorf("copy EFI system partition: %w", err)
		}
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
	confDir := filepath.Join(newroot, "etc", "mdadm")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", confDir, err)
	}
	conf := "# mdadm.conf - written by BOOTy\n\nDEVICE partitions\nHOMEHOST <system>\nMAILADDR root\n\n" + scan
	if err := os.WriteFile(filepath.Join(confDir, "mdadm.conf"), []byte(conf), 0o644); err != nil { //nolint:gosec // standard config file mode
		return fmt.Errorf("write mdadm.conf: %w", err)
	}
	cmd := "if command -v update-initramfs >/dev/null 2>&1; then update-initramfs -u -k all; " +
		"else dracut --force --regenerate-all; fi"
	if out, err := o.disk.ChrootRun(ctx, newroot, cmd); err != nil {
		return fmt.Errorf("rebuild initramfs for root mirror: %s: %w", string(out), err)
	}
	return nil
}

// createMirrorEFIBootEntry adds a firmware boot entry for the second disk so
// the host still boots when the first disk fails.
func (o *Orchestrator) createMirrorEFIBootEntry(ctx context.Context) error {
	if o.mirrorDisk == "" || o.bootPartition == "" {
		return nil
	}
	efiNum, err := disk.PartitionNumberChecked(o.bootPartition, o.targetDisk)
	if err != nil {
		return fmt.Errorf("determine EFI partition number: %w", err)
	}
	return o.config.CreateEFIBootEntry(ctx, o.mirrorDisk, blockdev.PartitionDevicePath(o.mirrorDisk, efiNum))
}
