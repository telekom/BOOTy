//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
// On failure the error includes truncated command output for diagnostics.
func (e *ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		raw := strings.TrimSpace(string(out))
		if len(raw) > 512 {
			raw = raw[:512] + "...(truncated)"
		}
		if raw != "" {
			return out, fmt.Errorf("exec %s: %w [output: %s]", name, err, raw)
		}
		return out, fmt.Errorf("exec %s: %w", name, err)
	}
	return out, nil
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
// This performs a quick erase: clears partition tables and filesystem signatures
// without overwriting data.
func (m *Manager) WipeAllDisks(ctx context.Context) error {
	slog.Info("Wiping all disk signatures (quick erase)")
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("reading /sys/block: %w", err)
	}
	var (
		wiped  int
		errs   []error
	)
	for _, entry := range entries {
		name := entry.Name()
		if isVirtualDisk(name) {
			continue
		}
		dev := "/dev/" + name
		slog.Info("Wiping disk", "device", dev)
		// Clear partition table with sgdisk --zap-all first, then wipefs.
		if out, err := m.cmd.Run(ctx, "sgdisk", "--zap-all", dev); err != nil {
			slog.Debug("sgdisk zap failed (may not be GPT)", "device", dev, "output", string(out))
		}
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			slog.Warn("wipefs failed", "device", dev, "output", string(out), "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", dev, err))
		} else {
			wiped++
		}
	}
	if wiped == 0 && len(errs) > 0 {
		return fmt.Errorf("all %d disk wipe(s) failed: %w", len(errs), errors.Join(errs...))
	}
	return nil
}

// SecureEraseAllDisks performs hardware-level secure erase on all disks.
// For NVMe drives: uses nvme format (User Data Erase).
// For SATA/SAS drives: uses ATA SECURITY ERASE UNIT via hdparm.
// Falls back to quick erase (wipefs) if secure erase is not supported.
func (m *Manager) SecureEraseAllDisks(ctx context.Context) error {
	slog.Info("Performing secure erase on all disks")
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("reading /sys/block: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if isVirtualDisk(name) {
			continue
		}
		dev := "/dev/" + name
		if strings.HasPrefix(name, "nvme") {
			m.secureEraseNVMe(ctx, dev)
		} else {
			m.secureEraseSATA(ctx, dev)
		}
	}
	return nil
}

// secureEraseNVMe performs NVMe User Data Erase via nvme format command.
func (m *Manager) secureEraseNVMe(ctx context.Context, dev string) {
	slog.Info("NVMe secure erase", "device", dev)
	// ses=1: User Data Erase, ses=2: Crypto Erase (not all drives support it).
	out, err := m.cmd.Run(ctx, "nvme", "format", dev, "--ses=1", "--force")
	if err != nil {
		slog.Warn("NVMe secure erase failed, falling back to wipefs",
			"device", dev, "output", string(out), "error", err)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			slog.Warn("wipefs fallback failed", "device", dev, "output", string(out))
		}
	}
}

// secureEraseSATA performs ATA SECURITY ERASE UNIT via hdparm.
func (m *Manager) secureEraseSATA(ctx context.Context, dev string) {
	slog.Info("SATA secure erase", "device", dev)
	// Step 1: Check if security is supported.
	out, err := m.cmd.Run(ctx, "hdparm", "-I", dev)
	if err != nil || !strings.Contains(string(out), "Security:") {
		slog.Info("Drive does not support ATA security, using wipefs", "device", dev)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			slog.Warn("wipefs fallback failed", "device", dev, "output", string(out))
		}
		return
	}
	if strings.Contains(string(out), "frozen") {
		slog.Warn("Drive is security-frozen, cannot secure erase, using wipefs", "device", dev)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			slog.Warn("wipefs fallback failed", "device", dev, "output", string(out))
		}
		return
	}
	// Step 2: Set a temporary password and issue secure erase.
	if out, err := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-set-pass", "Erase", dev); err != nil {
		slog.Warn("Failed to set security password", "device", dev, "output", string(out))
		return
	}
	if out, err := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-erase", "Erase", dev); err != nil {
		slog.Warn("ATA security erase failed", "device", dev, "output", string(out))
		// Clear the password we just set.
		if out2, err2 := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-disable", "Erase", dev); err2 != nil {
			slog.Warn("Failed to clear security password", "device", dev, "output", string(out2))
		}
	}
}

// CreateRAIDArray creates a software RAID array using mdadm.
// level is the RAID level (0, 1, 5, 6, 10). devices are the member disks.
func (m *Manager) CreateRAIDArray(ctx context.Context, name string, level int, devices []string) error {
	if len(devices) < 2 {
		return fmt.Errorf("RAID requires at least 2 devices, got %d", len(devices))
	}

	slog.Info("Creating RAID array", "name", name, "level", level, "devices", devices)

	args := make([]string, 0, 10+len(devices))
	args = append(args,
		"--create", "/dev/"+name,
		"--level", strconv.Itoa(level),
		"--raid-devices", strconv.Itoa(len(devices)),
		"--run",   // don't ask for confirmation
		"--force", // force creation
		"--metadata", "1.2",
	)
	args = append(args, devices...)

	out, err := m.cmd.Run(ctx, "mdadm", args...)
	if err != nil {
		return fmt.Errorf("mdadm create %s: %s: %w", name, string(out), err)
	}

	slog.Info("RAID array created", "name", name)
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
		if isVirtualDisk(name) {
			continue
		}

		sizeGB, err := readDiskSizeGB(name)
		if err != nil {
			continue
		}
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

// isVirtualDisk checks if a block device name is virtual (loop, cdrom, ram, device-mapper).
func isVirtualDisk(name string) bool {
	return strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "sr") ||
		strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-")
}

// readDiskSizeGB reads the size of a block device in GB from sysfs.
func readDiskSizeGB(name string) (int, error) {
	sizePath := "/sys/block/" + name + "/size" //nolint:gocritic // path join not needed for sysfs
	data, err := os.ReadFile(sizePath)
	if err != nil {
		return 0, fmt.Errorf("read disk size %s: %w", name, err)
	}
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse disk size %s: %w", name, err)
	}
	return int(sectors * 512 / (1024 * 1024 * 1024)), nil
}

// ParsePartitions reads the partition table using sfdisk --json.
func (m *Manager) ParsePartitions(ctx context.Context, disk string) ([]Partition, error) {
	out, err := m.cmd.Run(ctx, "sfdisk", "--json", disk)
	if err != nil {
		return nil, fmt.Errorf("sfdisk %s: %s: %w", disk, string(out), err)
	}
	var result sfdiskOutput
	if err := json.Unmarshal(out, &result); err != nil {
		raw := string(out)
		if len(raw) > 512 {
			raw = raw[:512] + "...(truncated)"
		}
		return nil, fmt.Errorf("parsing sfdisk output: %w [raw: %s]", err, raw)
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
// Supports ext2/3/4 (resize2fs), XFS (xfs_growfs), and btrfs.
func (m *Manager) ResizeFilesystem(ctx context.Context, device string) error {
	slog.Info("Resizing filesystem", "device", device)
	// Try resize2fs for ext2/3/4 first.
	if out, err := m.cmd.Run(ctx, "resize2fs", device); err != nil {
		slog.Debug("resize2fs failed, trying xfs_growfs", "output", string(out))
		// Fall back to xfs_growfs.
		if out, err := m.cmd.Run(ctx, "xfs_growfs", device); err != nil {
			slog.Debug("xfs_growfs failed, trying btrfs resize", "output", string(out))
			// Fall back to btrfs filesystem resize.
			if out, err := m.cmd.Run(ctx, "btrfs", "filesystem", "resize", "max", device); err != nil {
				return fmt.Errorf("resize filesystem %s: %s: %w", device, string(out), err)
			}
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

// supportedFilesystems lists filesystem types to try when mounting, in priority order.
var supportedFilesystems = []string{"ext4", "xfs", "btrfs", "ext3", "ext2", "vfat"}

// MountPartition mounts a device at the given mountpoint.
// Tries all supported filesystem types in order: ext4, xfs, btrfs, ext3, ext2, vfat.
func (m *Manager) MountPartition(_ context.Context, device, mountpoint string) error {
	slog.Info("Mounting partition", "device", device, "mountpoint", mountpoint)
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("creating mountpoint %s: %w", mountpoint, err)
	}
	var errs []string
	for _, fsType := range supportedFilesystems {
		err := syscall.Mount(device, mountpoint, fsType, 0, "")
		if err == nil {
			slog.Info("Mounted partition", "device", device, "fsType", fsType)
			return nil
		}
		errs = append(errs, fmt.Sprintf("%s=%v", fsType, err))
	}
	return fmt.Errorf("mounting %s at %s: tried %s", device, mountpoint, strings.Join(errs, ", "))
}

// BindMount performs a bind mount.
func (m *Manager) BindMount(source, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("creating bind mount target %s: %w", target, err)
	}
	if err := syscall.Mount(source, target, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind mount %s -> %s: %w", source, target, err)
	}
	return nil
}

// Unmount unmounts a filesystem.
func (m *Manager) Unmount(target string) error {
	if err := syscall.Unmount(target, 0); err != nil {
		return fmt.Errorf("unmount %s: %w", target, err)
	}
	return nil
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

// DisableLVM deactivates LVM volume groups before disk wipe.
func (m *Manager) DisableLVM(ctx context.Context) error {
	slog.Info("Deactivating LVM volume groups")
	out, err := m.cmd.Run(ctx, "lvm", "vgchange", "-an")
	if err != nil {
		// Not fatal — LVM may not be present.
		slog.Debug("lvm deactivate (may be expected if no LVM)", "output", string(out), "error", err)
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
// When using a mock commander (tests), the command is routed through the commander.
// For real execution, the external chroot binary is used. If the chroot binary
// is not found (e.g., in minimal initramfs), falls back to syscall-based chroot.
func (m *Manager) ChrootRun(ctx context.Context, root, command string) ([]byte, error) {
	out, err := m.cmd.Run(ctx, "chroot", root, "/bin/bash", "-c", command)
	if err != nil {
		// If chroot binary is missing, fall back to syscall-based chroot.
		if isExecNotFound(err) {
			slog.Info("chroot binary not found, using syscall fallback", "root", root)
			return m.chrootSyscall(ctx, root, command)
		}
		return out, fmt.Errorf("chroot exec in %s: %s: %w", root, strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

// chrootSyscall runs a command using SysProcAttr.Chroot instead of the chroot binary.
func (m *Manager) chrootSyscall(ctx context.Context, root, command string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("chroot syscall in %s: %s: %w", root, strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

// isExecNotFound checks whether an error indicates the executable was not found.
func isExecNotFound(err error) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	// The Commander wraps errors, so also check the message.
	return strings.Contains(err.Error(), "executable file not found")
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

	// MS_BIND is non-recursive — efivarfs (under /sys) is not propagated.
	// Bind it separately so chroot efibootmgr can access EFI variables.
	efiSrc := "/sys/firmware/efi/efivars"
	efiDst := root + "/sys/firmware/efi/efivars"
	if _, err := os.Stat(efiSrc); err == nil {
		if err := os.MkdirAll(efiDst, 0o755); err != nil {
			slog.Debug("Cannot create efivarfs mount target", "path", efiDst, "error", err)
		} else if err := m.BindMount(efiSrc, efiDst); err != nil {
			slog.Debug("Cannot bind-mount efivarfs into chroot", "error", err)
		}
	}
	return nil
}

// TeardownChrootBindMounts unmounts standard bind mounts.
// Errors are logged to aid debugging of stale mount points.
func (m *Manager) TeardownChrootBindMounts(root string) {
	// Unmount efivarfs first (sub-mount under /sys).
	efiPath := root + "/sys/firmware/efi/efivars"
	if err := m.Unmount(efiPath); err != nil {
		slog.Debug("Chroot efivarfs unmount skipped", "path", efiPath, "error", err)
	}
	for _, rel := range []string{"run", "sys", "proc", "dev"} {
		if err := m.Unmount(root + "/" + rel); err != nil {
			slog.Warn("Chroot unmount failed", "path", root+"/"+rel, "error", err)
		}
	}
}
