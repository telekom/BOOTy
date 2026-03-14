// lvm.go implements LVM volume group and logical volume management.
//go:build linux

package disk

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/config"
)

// ApplyLVMConfig creates PV, VG, and LVs according to the LVM config.
func (m *Manager) ApplyLVMConfig(ctx context.Context, device string, layout *config.PartitionLayout) error {
	if layout.LVM == nil || len(layout.LVM.Volumes) == 0 {
		return nil
	}

	lvm := layout.LVM
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
		if vol.Extents != "" {
			args = append(args, "-l", vol.Extents)
		} else if vol.SizeMB > 0 {
			args = append(args, "-L", fmt.Sprintf("%dM", vol.SizeMB))
		} else {
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
	var sb fmt.Stringer = &fstabBuilder{}
	b := sb.(*fstabBuilder)
	for _, vol := range lvm.Volumes {
		if vol.Mountpoint == "" {
			continue
		}
		lvDev := fmt.Sprintf("/dev/%s/%s", lvm.VolumeGroup, vol.Name)
		fsType := vol.Filesystem
		if fsType == "" {
			fsType = "auto"
		}
		pass := 2
		if vol.Mountpoint == "/" {
			pass = 1
		}
		b.addEntry(lvDev, vol.Mountpoint, fsType, "defaults", 0, pass)
	}
	return b.String()
}

type fstabBuilder struct {
	entries []string
}

func (b *fstabBuilder) addEntry(dev, mount, fs, opts string, dump, pass int) {
	b.entries = append(b.entries, fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d", dev, mount, fs, opts, dump, pass))
}

func (b *fstabBuilder) String() string {
	result := ""
	for _, e := range b.entries {
		result += e + "\n"
	}
	return result
}
