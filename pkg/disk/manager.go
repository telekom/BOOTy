//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Commander abstracts command execution for testing.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecCommander executes real system commands.
type ExecCommander struct{}

// Run executes a system command and returns its combined output.
func (e *ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Manager handles disk operations for provisioning.
type Manager struct {
	cmd Commander
}

// NewManager creates a Manager with the given Commander (nil = ExecCommander).
func NewManager(cmd Commander) *Manager {
	if cmd == nil {
		cmd = &ExecCommander{}
	}
	return &Manager{cmd: cmd}
}

// Partition represents a single disk partition from sfdisk output.
type Partition struct {
	Node  string `json:"node"`
	Start int64  `json:"start"`
	Size  int64  `json:"size"`
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
}

// sfdiskOutput represents the JSON output of sfdisk --json.
type sfdiskOutput struct {
	PartitionTable struct {
		Partitions []Partition `json:"partitions"`
	} `json:"partitiontable"`
}

// EFI and Linux partition type GUIDs.
const (
	EFISystemPartitionGUID = "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
	LinuxFilesystemGUID    = "0FC63DAF-8483-4772-8E79-3D69D8477DE4"
)

// StopRAIDArrays stops all RAID arrays via mdadm.
func (m *Manager) StopRAIDArrays(ctx context.Context) error {
	slog.Info("Stopping RAID arrays")
	out, err := m.cmd.Run(ctx, "mdadm", "--stop", "--scan")
	if err != nil {
		// mdadm returns error if no arrays found, which is fine.
		slog.Debug("mdadm stop (may be expected if no arrays)", "output", string(out), "error", err)
	}
	return nil
}

// WipeAllDisks runs wipefs on all block devices excluding loop and CD-ROM.
func (m *Manager) WipeAllDisks(ctx context.Context) error {
	slog.Info("Wiping all disk signatures")
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("reading /sys/block: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "sr") ||
			strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		dev := "/dev/" + name
		slog.Info("Wiping disk", "device", dev)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			slog.Warn("wipefs failed", "device", dev, "output", string(out), "error", err)
		}
	}
	return nil
}

// DetectDisk finds the target disk for provisioning. Prefers NVMe, falls back to SATA/SAS.
func (m *Manager) DetectDisk(_ context.Context, minSizeGB int) (string, error) {
	slog.Info("Detecting target disk", "minSizeGB", minSizeGB)
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return "", fmt.Errorf("reading /sys/block: %w", err)
	}

	type candidate struct {
		path   string
		sizeGB int
		isNVMe bool
	}
	var candidates []candidate

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "sr") ||
			strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}

		sizePath := filepath.Join("/sys/block", name, "size")
		data, err := os.ReadFile(sizePath)
		if err != nil {
			continue
		}
		sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			continue
		}
		sizeGB := int(sectors * 512 / (1024 * 1024 * 1024))
		if minSizeGB > 0 && sizeGB < minSizeGB {
			continue
		}
		candidates = append(candidates, candidate{
			path:   "/dev/" + name,
			sizeGB: sizeGB,
			isNVMe: strings.HasPrefix(name, "nvme"),
		})
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no suitable disk found (min %d GB)", minSizeGB)
	}

	// Prefer NVMe, otherwise pick largest.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.isNVMe && !best.isNVMe {
			best = c
		} else if c.isNVMe == best.isNVMe && c.sizeGB > best.sizeGB {
			best = c
		}
	}

	slog.Info("Selected disk", "device", best.path, "sizeGB", best.sizeGB, "nvme", best.isNVMe)
	return best.path, nil
}

// ParsePartitions reads the partition table using sfdisk --json.
func (m *Manager) ParsePartitions(ctx context.Context, disk string) ([]Partition, error) {
	out, err := m.cmd.Run(ctx, "sfdisk", "--json", disk)
	if err != nil {
		return nil, fmt.Errorf("sfdisk %s: %s: %w", disk, string(out), err)
	}
	var result sfdiskOutput
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing sfdisk output: %w", err)
	}
	return result.PartitionTable.Partitions, nil
}

// FindBootPartition finds the EFI System Partition.
func (m *Manager) FindBootPartition(parts []Partition) (*Partition, error) {
	for i := range parts {
		if strings.EqualFold(parts[i].Type, EFISystemPartitionGUID) {
			return &parts[i], nil
		}
	}
	return nil, fmt.Errorf("no EFI system partition found")
}

// FindRootPartition finds the primary Linux filesystem partition.
func (m *Manager) FindRootPartition(parts []Partition) (*Partition, error) {
	for i := range parts {
		if strings.EqualFold(parts[i].Type, LinuxFilesystemGUID) {
			return &parts[i], nil
		}
	}
	return nil, fmt.Errorf("no Linux filesystem partition found")
}

// GrowPartition grows a partition to fill available space using growpart.
func (m *Manager) GrowPartition(ctx context.Context, disk string, partNum int) error {
	slog.Info("Growing partition", "disk", disk, "partition", partNum)
	out, err := m.cmd.Run(ctx, "growpart", disk, strconv.Itoa(partNum))
	if err != nil {
		// growpart exits 1 if partition already fills the disk.
		if strings.Contains(string(out), "NOCHANGE") {
			slog.Info("Partition already at max size")
			return nil
		}
		return fmt.Errorf("growpart %s %d: %s: %w", disk, partNum, string(out), err)
	}
	return nil
}

// ResizeFilesystem resizes the filesystem on the given device.
func (m *Manager) ResizeFilesystem(ctx context.Context, device string) error {
	slog.Info("Resizing filesystem", "device", device)
	// Try resize2fs for ext4 first.
	if out, err := m.cmd.Run(ctx, "resize2fs", device); err != nil {
		slog.Debug("resize2fs failed, trying xfs_growfs", "output", string(out))
		// Fall back to xfs_growfs.
		if out, err := m.cmd.Run(ctx, "xfs_growfs", device); err != nil {
			return fmt.Errorf("resize filesystem %s: %s: %w", device, string(out), err)
		}
	}
	return nil
}

// PartProbe re-reads partition table.
func (m *Manager) PartProbe(ctx context.Context, disk string) error {
	slog.Info("Re-reading partition table", "disk", disk)
	out, err := m.cmd.Run(ctx, "partprobe", disk)
	if err != nil {
		return fmt.Errorf("partprobe %s: %s: %w", disk, string(out), err)
	}
	return nil
}

// MountPartition mounts a device at the given mountpoint.
func (m *Manager) MountPartition(_ context.Context, device, mountpoint string) error {
	slog.Info("Mounting partition", "device", device, "mountpoint", mountpoint)
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("creating mountpoint %s: %w", mountpoint, err)
	}
	if err := syscall.Mount(device, mountpoint, "ext4", 0, ""); err != nil {
		// Try xfs if ext4 fails.
		if err2 := syscall.Mount(device, mountpoint, "xfs", 0, ""); err2 != nil {
			return fmt.Errorf("mounting %s at %s: ext4=%v xfs=%w", device, mountpoint, err, err2)
		}
	}
	return nil
}

// BindMount performs a bind mount.
func (m *Manager) BindMount(source, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("creating bind mount target %s: %w", target, err)
	}
	return syscall.Mount(source, target, "", syscall.MS_BIND, "")
}

// Unmount unmounts a filesystem.
func (m *Manager) Unmount(target string) error {
	return syscall.Unmount(target, 0)
}

// CheckFilesystem runs e2fsck on the device.
func (m *Manager) CheckFilesystem(ctx context.Context, device string) error {
	slog.Info("Checking filesystem", "device", device)
	out, err := m.cmd.Run(ctx, "e2fsck", "-fy", device)
	if err != nil {
		slog.Warn("e2fsck returned non-zero (may have fixed errors)", "device", device, "output", string(out))
	}
	return nil
}

// EnableLVM activates LVM volume groups.
func (m *Manager) EnableLVM(ctx context.Context) error {
	slog.Info("Activating LVM volume groups")
	out, err := m.cmd.Run(ctx, "lvm", "vgchange", "-ay")
	if err != nil {
		return fmt.Errorf("lvm vgchange: %s: %w", string(out), err)
	}
	return nil
}

// ChrootRun executes a command in a chroot environment.
func (m *Manager) ChrootRun(ctx context.Context, root, command string) ([]byte, error) {
	return m.cmd.Run(ctx, "chroot", root, "/bin/bash", "-c", command)
}

// SetupChrootBindMounts creates standard bind mounts for chroot operations.
func (m *Manager) SetupChrootBindMounts(root string) error {
	binds := []struct{ src, rel string }{
		{"/dev", "dev"},
		{"/proc", "proc"},
		{"/sys", "sys"},
		{"/run", "run"},
	}
	for _, b := range binds {
		if err := m.BindMount(b.src, root+"/"+b.rel); err != nil {
			return fmt.Errorf("bind mount %s: %w", b.src, err)
		}
	}
	return nil
}

// TeardownChrootBindMounts unmounts standard bind mounts.
func (m *Manager) TeardownChrootBindMounts(root string) {
	for _, rel := range []string{"run", "sys", "proc", "dev"} {
		_ = m.Unmount(root + "/" + rel)
	}
}
