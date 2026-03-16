//go:build linux

// lvm.go implements LVM volume group and logical volume management.

package disk

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
)

// ApplyLVMConfig creates PV, VG, and LVs according to the LVM config.
func (m *Manager) ApplyLVMConfig(ctx context.Context, device string, layout *config.PartitionLayout) error {
	if layout == nil || layout.LVM == nil || len(layout.LVM.Volumes) == 0 {
		return nil
	}

	lvm := layout.LVM
	if lvm.PVPartition < 1 {
		return fmt.Errorf("lvm.pvPartition must be >= 1, got %d", lvm.PVPartition)
	}
	if lvm.PVPartition > len(layout.Partitions) {
		return fmt.Errorf("lvm.pvPartition %d exceeds partition count %d", lvm.PVPartition, len(layout.Partitions))
	}
	pvDev := partitionDevice(device, lvm.PVPartition)

	slog.Info("Setting up LVM", "vg", lvm.VolumeGroup, "pv", pvDev, "volumes", len(lvm.Volumes))

	// Create physical volume.
	if out, err := m.cmd.Run(ctx, "pvcreate", "-f", pvDev); err != nil {
		return fmt.Errorf("pvcreate %s: %s: %w", pvDev, string(out), err)
	}

	// Create volume group.
	if out, err := m.cmd.Run(ctx, "vgcreate", lvm.VolumeGroup, pvDev); err != nil {
		return fmt.Errorf("vgcreate %s: %s: %w", lvm.VolumeGroup, string(out), err)
	}

	// Create logical volumes.
	for _, vol := range lvm.Volumes {
		args := []string{}
		switch {
		case vol.Extents != "":
			args = append(args, "-l", vol.Extents)
		case vol.SizeMB > 0:
			args = append(args, "-L", fmt.Sprintf("%dM", vol.SizeMB))
		default:
			args = append(args, "-l", "100%FREE")
		}
		args = append(args, "-n", vol.Name, lvm.VolumeGroup)

		if out, err := m.cmd.Run(ctx, "lvcreate", args...); err != nil {
			return fmt.Errorf("lvcreate %s/%s: %s: %w", lvm.VolumeGroup, vol.Name, string(out), err)
		}
		slog.Info("Created logical volume", "vg", lvm.VolumeGroup, "lv", vol.Name)

		// Format LV if filesystem specified.
		if vol.Filesystem != "" {
			lvDev := fmt.Sprintf("/dev/%s/%s", lvm.VolumeGroup, vol.Name)
			if err := m.formatPartition(ctx, lvDev, vol.Filesystem); err != nil {
				return fmt.Errorf("formatting LV %s: %w", vol.Name, err)
			}
		}
	}

	return nil
}

// GenerateLVMFstab adds LVM volume fstab entries.
func GenerateLVMFstab(lvm *config.LVMConfig) string {
	if lvm == nil {
		return ""
	}
	var sb strings.Builder
	for _, vol := range lvm.Volumes {
		lvDev := fmt.Sprintf("/dev/%s/%s", lvm.VolumeGroup, vol.Name)
		// Swap volumes have no mountpoint but must appear in fstab as "none swap sw".
		if vol.Filesystem == "swap" {
			fmt.Fprintf(&sb, "%s\tnone\tswap\tsw\t0\t0\n", lvDev)
			continue
		}
		if vol.Mountpoint == "" {
			continue
		}
		fsType := vol.Filesystem
		if fsType == "" {
			fsType = "auto"
		}
		pass := 2
		if vol.Mountpoint == "/" {
			pass = 1
		}
		fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\t%d\t%d\n", lvDev, vol.Mountpoint, fsType, "defaults", 0, pass)
	}
	return sb.String()
}
