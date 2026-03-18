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
	lvm, pvDev, err := validateLVMApplyInputs(device, layout)
	if err != nil {
		return err
	}
	if lvm == nil {
		return nil
	}

	slog.Info("setting up lvm", "vg", lvm.VolumeGroup, "pv", pvDev, "volumes", len(lvm.Volumes))

	if err := m.createPhysicalVolume(ctx, pvDev); err != nil {
		return err
	}

	if err := m.createVolumeGroup(ctx, lvm.VolumeGroup, pvDev); err != nil {
		return err
	}

	for _, vol := range lvm.Volumes {
		if err := m.createLogicalVolume(ctx, lvm.VolumeGroup, vol); err != nil {
			return err
		}
		slog.Info("created logical volume", "vg", lvm.VolumeGroup, "lv", vol.Name)

		if err := m.formatLogicalVolume(ctx, lvm.VolumeGroup, vol); err != nil {
			return err
		}
	}

	return nil
}

func validateLVMApplyInputs(device string, layout *config.PartitionLayout) (*config.LVMConfig, string, error) {
	if layout == nil || layout.LVM == nil || len(layout.LVM.Volumes) == 0 {
		return nil, "", nil
	}
	if device == "" {
		return nil, "", fmt.Errorf("lvm device path is empty")
	}

	lvm := layout.LVM
	if lvm.PVPartition < 1 {
		return nil, "", fmt.Errorf("lvm.pvPartition must be >= 1, got %d", lvm.PVPartition)
	}
	if lvm.PVPartition > len(layout.Partitions) {
		return nil, "", fmt.Errorf("lvm.pvPartition %d exceeds partition count %d", lvm.PVPartition, len(layout.Partitions))
	}
	vgName := strings.TrimSpace(lvm.VolumeGroup)
	if vgName == "" {
		return nil, "", fmt.Errorf("lvm volumeGroup name is empty")
	}
	if !isValidLVMRuntimeName(vgName) {
		return nil, "", fmt.Errorf("lvm volumeGroup name %q is invalid", lvm.VolumeGroup)
	}
	for i, vol := range lvm.Volumes {
		lvName := strings.TrimSpace(vol.Name)
		if lvName == "" {
			return nil, "", fmt.Errorf("lvm volume %d: name is empty", i+1)
		}
		if !isValidLVMRuntimeName(lvName) {
			return nil, "", fmt.Errorf("lvm volume %d: invalid name %q", i+1, vol.Name)
		}
	}

	return lvm, partitionDevice(device, lvm.PVPartition), nil
}

func isValidLVMRuntimeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func (m *Manager) createPhysicalVolume(ctx context.Context, pvDev string) error {
	out, err := m.cmd.Run(ctx, "pvcreate", "-f", pvDev)
	if err != nil {
		return fmt.Errorf("pvcreate %s: %s: %w", pvDev, string(out), err)
	}
	return nil
}

func (m *Manager) createVolumeGroup(ctx context.Context, vg, pvDev string) error {
	out, err := m.cmd.Run(ctx, "vgcreate", vg, pvDev)
	if err != nil {
		return fmt.Errorf("vgcreate %s: %s: %w", vg, string(out), err)
	}
	return nil
}

func (m *Manager) createLogicalVolume(ctx context.Context, vg string, vol config.LVVolume) error {
	out, err := m.cmd.Run(ctx, "lvcreate", buildLVCreateArgs(vg, vol)...)
	if err != nil {
		return fmt.Errorf("lvcreate %s/%s: %s: %w", vg, vol.Name, string(out), err)
	}
	return nil
}

func buildLVCreateArgs(vg string, vol config.LVVolume) []string {
	args := make([]string, 0, 6)
	switch {
	case vol.Extents != "":
		args = append(args, "-l", vol.Extents)
	case vol.SizeMB > 0:
		args = append(args, "-L", fmt.Sprintf("%dM", vol.SizeMB))
	default:
		args = append(args, "-l", "100%FREE")
	}
	return append(args, "-n", vol.Name, vg)
}

func (m *Manager) formatLogicalVolume(ctx context.Context, vg string, vol config.LVVolume) error {
	if vol.Filesystem == "" {
		return nil
	}
	lvDev := fmt.Sprintf("/dev/%s/%s", vg, vol.Name)
	if err := m.formatPartition(ctx, lvDev, vol.Filesystem); err != nil {
		return fmt.Errorf("formatting LV %s: %w", vol.Name, err)
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
