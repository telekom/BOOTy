//go:build linux

package disk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/telekom/BOOTy/pkg/executil"
)

// Commander abstracts command execution for testing.
type Commander = executil.Commander

// ExecCommander executes real system commands.
type ExecCommander = executil.ExecCommander

// Manager handles disk operations for provisioning.
type Manager struct {
	cmd       Commander
	sysfsRoot string // path to sysfs root, defaults to "/sys"
}

// NewManager creates a Manager with the given Commander (nil = ExecCommander).
func NewManager(cmd Commander) *Manager {
	if cmd == nil {
		cmd = &ExecCommander{}
	}
	return &Manager{cmd: cmd, sysfsRoot: "/sys"}
}

// newManagerWithSysfs creates a Manager with a custom sysfs root for testing.
func newManagerWithSysfs(cmd Commander, sysfsRoot string) *Manager {
	if cmd == nil {
		cmd = &ExecCommander{}
	}
	return &Manager{cmd: cmd, sysfsRoot: sysfsRoot}
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
	slog.Info("stopping RAID arrays")
	out, err := m.cmd.Run(ctx, "mdadm", "--stop", "--scan")
	if err != nil {
		if isNoArraysFound(out, err) {
			slog.Debug("mdadm stop reported no arrays", "output", strings.TrimSpace(string(out)), "error", err)
			return nil
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return fmt.Errorf("stop raid arrays: %w", err)
		}
		return fmt.Errorf("stop raid arrays: %s: %w", trimmed, err)
	}
	return nil
}

func isNoArraysFound(out []byte, err error) bool {
	combined := strings.ToLower(strings.TrimSpace(string(out)))
	if err != nil {
		combined = strings.TrimSpace(combined + " " + strings.ToLower(err.Error()))
	}
	return strings.Contains(combined, "no arrays found")
}

// WipeDisk clears partition-table and filesystem signatures on a single device.
// This is a quick erase (no data overwrite) suitable for pre-streaming cleanup.
func (m *Manager) WipeDisk(ctx context.Context, device string) error {
	slog.Info("wiping disk signatures", "device", device)
	if out, err := m.cmd.Run(ctx, "sgdisk", "--zap-all", device); err != nil {
		slog.Debug("sgdisk zap failed (may not be GPT)", "device", device, "output", string(out))
	}
	if _, err := m.cmd.Run(ctx, "wipefs", "-af", device); err != nil {
		return fmt.Errorf("wipefs %s: %w", device, err)
	}
	return nil
}

// WipeAllDisks runs wipefs on all block devices excluding loop and CD-ROM.
// This performs a quick erase: clears partition tables and filesystem signatures
// without overwriting data.
func (m *Manager) WipeAllDisks(ctx context.Context) error {
	slog.Info("wiping all disk signatures (quick erase)")
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("reading /sys/block: %w", err)
	}
	var (
		wiped int
		errs  []error
	)
	for _, entry := range entries {
		name := entry.Name()
		if isVirtualDisk(name) {
			continue
		}
		dev := "/dev/" + name
		slog.Info("wiping disk", "device", dev)
		// Clear partition table with sgdisk --zap-all first, then wipefs.
		if out, err := m.cmd.Run(ctx, "sgdisk", "--zap-all", dev); err != nil {
			slog.Debug("sgdisk zap failed (may not be GPT)", "device", dev, "output", string(out))
		}
		if _, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			slog.Warn("wipefs failed", "device", dev, "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", dev, err))
		} else {
			wiped++
		}
	}
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		if wiped == 0 {
			return fmt.Errorf("all %d disk wipe(s) failed: %s", len(errs), strings.Join(msgs, "; "))
		}
		return fmt.Errorf("wiped %d disk(s) but %d failed: %s", wiped, len(errs), strings.Join(msgs, "; "))
	}
	return nil
}

// SecureEraseAllDisks performs hardware-level secure erase on all disks.
// For NVMe drives: uses nvme format (User Data Erase).
// For SATA/SAS drives: uses ATA SECURITY ERASE UNIT via hdparm.
// Falls back to quick erase (wipefs) if secure erase is not supported.
func (m *Manager) SecureEraseAllDisks(ctx context.Context) error {
	slog.Info("performing secure erase on all disks")
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return fmt.Errorf("reading /sys/block: %w", err)
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if isVirtualDisk(name) {
			continue
		}
		dev := "/dev/" + name
		if strings.HasPrefix(name, "nvme") {
			if err := m.secureEraseNVMe(ctx, dev); err != nil {
				errs = append(errs, err)
			}
		} else {
			if err := m.secureEraseSATA(ctx, dev); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("secure erase failed on %d disk(s): %s", len(errs), strings.Join(msgs, "; "))
	}
	return nil
}

// secureEraseNVMe performs NVMe User Data Erase via nvme format command.
func (m *Manager) secureEraseNVMe(ctx context.Context, dev string) error {
	slog.Info("NVMe secure erase", "device", dev)
	// ses=1: User Data Erase, ses=2: Crypto Erase (not all drives support it).
	out, err := m.cmd.Run(ctx, "nvme", "format", dev, "--ses=1", "--force")
	if err != nil {
		slog.Warn("NVMe secure erase failed, falling back to wipefs",
			"device", dev, "output", string(out), "error", err)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			return fmt.Errorf("%s: nvme format and wipefs fallback failed: %s: %w", dev, string(out), err)
		}
	}
	return nil
}

// secureEraseSATA performs ATA SECURITY ERASE UNIT via hdparm.
func (m *Manager) secureEraseSATA(ctx context.Context, dev string) error {
	slog.Info("SATA secure erase", "device", dev)
	pwd, err := temporaryErasePassword()
	if err != nil {
		return fmt.Errorf("generate temporary erase password for %s: %w", dev, err)
	}

	// Step 1: Check if security is supported.
	out, err := m.cmd.Run(ctx, "hdparm", "-I", dev)
	if err != nil || !strings.Contains(string(out), "Security:") {
		slog.Info("drive does not support ATA security, using wipefs", "device", dev)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			return fmt.Errorf("%s: hdparm and wipefs fallback failed: %s: %w", dev, string(out), err)
		}
		return nil
	}
	if strings.Contains(string(out), "frozen") {
		slog.Warn("drive is security-frozen, cannot secure erase, using wipefs", "device", dev)
		if out, err := m.cmd.Run(ctx, "wipefs", "-af", dev); err != nil {
			return fmt.Errorf("%s: frozen drive wipefs fallback failed: %s: %w", dev, string(out), err)
		}
		return nil
	}
	// Step 2: Set a temporary password and issue secure erase.
	if out, err := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-set-pass", pwd, dev); err != nil {
		return fmt.Errorf("%s: failed to set security password: %s: %w", dev, string(out), err)
	}
	if out, err := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-erase", pwd, dev); err != nil {
		slog.Warn("ATA security erase failed", "device", dev, "output", string(out))
		// Clear the password we just set.
		if out2, err2 := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-disable", pwd, dev); err2 != nil {
			slog.Warn("failed to clear security password", "device", dev, "output", string(out2))
		}
		return fmt.Errorf("%s: ATA security erase failed: %w", dev, err)
	}
	if out, err := m.cmd.Run(ctx, "hdparm", "--user-master", "u", "--security-disable", pwd, dev); err != nil {
		slog.Warn("failed to clear temporary security password after erase", "device", dev, "output", string(out), "error", err)
	}
	return nil
}

func temporaryErasePassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return "Erase-" + hex.EncodeToString(b), nil
}

// CreateRAIDArray creates a software RAID array using mdadm.
// level is the RAID level (0, 1, 5, 6, 10). devices are the member disks.
func (m *Manager) CreateRAIDArray(ctx context.Context, name string, level int, devices []string) error {
	if len(devices) < 2 {
		return fmt.Errorf("RAID requires at least 2 devices, got %d", len(devices))
	}

	slog.Info("creating RAID array", "name", name, "level", level, "devices", devices)

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
// Removable media (USB drives, SD cards) are rejected unless BOOTY_ALLOW_REMOVABLE=true.
func (m *Manager) DetectDisk(_ context.Context, minSizeGB int) (string, error) {
	slog.Info("detecting target disk", "minSizeGB", minSizeGB)
	blockDir := m.sysfsRoot + "/block"
	entries, err := os.ReadDir(blockDir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", blockDir, err)
	}

	allowRemovable := os.Getenv("BOOTY_ALLOW_REMOVABLE") == "true"

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

		if !allowRemovable && m.isRemovableMedia(name) {
			slog.Warn("skipping removable media device", "device", "/dev/"+name)
			continue
		}

		sizeGB, err := m.readDiskSizeGB(name)
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

	slog.Info("selected disk", "device", best.path, "sizeGB", best.sizeGB, "nvme", best.isNVMe)
	return best.path, nil
}

// isRemovableMedia reports whether the block device is removable (USB, SD card).
// Fails closed: if the sysfs attribute cannot be read, the device is treated as
// removable to err on the side of caution.
func (m *Manager) isRemovableMedia(name string) bool {
	path := m.sysfsRoot + "/block/" + name + "/removable"
	data, err := os.ReadFile(path) //nolint:gocritic // path join not needed for sysfs
	if err != nil {
		slog.Warn("cannot read removable attribute; treating as removable", "device", name, "err", err)
		return true
	}
	return strings.TrimSpace(string(data)) == "1"
}

// isVirtualDisk checks if a block device name is virtual and should be
// excluded from physical disk selection. Covers: loop devices, CD-ROMs,
// RAM disks, compressed RAM (zram), device-mapper, software RAID (md),
// ZFS zvols (zd), and network block devices (nbd).
func isVirtualDisk(name string) bool {
	return strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "sr") ||
		strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") ||
		strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "md") ||
		strings.HasPrefix(name, "zd") || strings.HasPrefix(name, "nbd")
}

// readDiskSizeGB reads the size of a block device in GB from sysfs.
func (m *Manager) readDiskSizeGB(name string) (int, error) {
	sizePath := m.sysfsRoot + "/block/" + name + "/size" //nolint:gocritic // path join not needed for sysfs
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
//
// sfdisk may emit non-JSON warnings (e.g. "GPT PMBR size mismatch") on
// stderr which CombinedOutput merges before the JSON body. We strip
// everything before the first '{' so the JSON decoder sees clean input.
func (m *Manager) ParsePartitions(ctx context.Context, disk string) ([]Partition, error) {
	out, err := m.cmd.Run(ctx, "sfdisk", "--json", disk)
	if err != nil {
		// A disk with no partition table is not an error — return empty list
		// so the caller can decide what to do (e.g. create partitions).
		if strings.Contains(string(out), "does not contain a recognized partition table") {
			slog.Info("disk has no partition table", "disk", disk)
			return nil, nil
		}
		return nil, fmt.Errorf("sfdisk %s: %w", disk, err)
	}

	jsonBytes := stripNonJSONPrefix(out)

	var result sfdiskOutput
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		raw := strings.Join(strings.Fields(string(out)), " ")
		if len(raw) > 512 {
			raw = raw[:512] + "...(truncated)"
		}
		return nil, fmt.Errorf("parsing sfdisk output: %w [raw: %s]", err, raw)
	}
	return result.PartitionTable.Partitions, nil
}

// stripNonJSONPrefix returns the slice starting at the first '{'.
// sfdisk --json may prepend stderr warnings (e.g. GPT geometry messages)
// when captured via CombinedOutput.
func stripNonJSONPrefix(b []byte) []byte {
	if idx := bytes.IndexByte(b, '{'); idx > 0 {
		return b[idx:]
	}
	return b
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
// When multiple partitions share the LinuxFilesystemGUID, it prefers one
// with a "root" or "/" GPT name, otherwise returns the last match (root
// is typically after /boot in the partition table).
func (m *Manager) FindRootPartition(parts []Partition) (*Partition, error) {
	var last *Partition
	for i := range parts {
		if !strings.EqualFold(parts[i].Type, LinuxFilesystemGUID) {
			continue
		}
		lower := strings.ToLower(parts[i].Name)
		if lower == "root" || lower == "/" {
			return &parts[i], nil
		}
		last = &parts[i]
	}
	if last != nil {
		return last, nil
	}
	return nil, fmt.Errorf("no Linux filesystem partition found")
}

// GrowPartition grows a partition to fill available space using growpart.
func (m *Manager) GrowPartition(ctx context.Context, disk string, partNum int) error {
	slog.Info("growing partition", "disk", disk, "partition", partNum)
	out, err := m.cmd.Run(ctx, "growpart", disk, strconv.Itoa(partNum))
	if err != nil {
		// growpart exits 1 if partition already fills the disk.
		if strings.Contains(string(out), "NOCHANGE") {
			slog.Info("partition already at max size")
			return nil
		}
		return fmt.Errorf("growpart %s %d: %s: %w", disk, partNum, string(out), err)
	}
	return nil
}

// ResizeFilesystem resizes the filesystem on the given device.
// Supports ext2/3/4 (resize2fs), XFS (xfs_growfs), and btrfs.
func (m *Manager) ResizeFilesystem(ctx context.Context, device string) error {
	slog.Info("resizing filesystem", "device", device)
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

// PartProbe re-reads partition table and triggers device node creation.
//
// Calls partprobe first; falls back to blockdev --rereadpt if partprobe fails.
// After re-reading, runs mdev -s (busybox mini-udev) to ensure partition
// device nodes are created — devtmpfs alone may not create them in minimal
// initramfs environments without udevd.
func (m *Manager) PartProbe(ctx context.Context, disk string) error {
	slog.Info("re-reading partition table", "disk", disk)

	// Flush all pending writes to the block device backend before re-reading
	// the partition table. On QEMU/KVM, BLKRRPART may invalidate the page
	// cache and read stale data from the virtual disk if writeback is pending.
	if syncOut, syncErr := m.cmd.Run(ctx, "sync"); syncErr != nil {
		slog.Warn("sync before partprobe failed", "error", syncErr, "output", string(syncOut))
	}

	// Diagnostic: show device state before partprobe.
	logDeviceState(disk, "before-partprobe")

	out, err := m.cmd.Run(ctx, "partprobe", disk)
	if err != nil {
		slog.Warn("partprobe failed, trying blockdev --rereadpt", "disk", disk, "error", err, "output", string(out))
		out, err = m.cmd.Run(ctx, "blockdev", "--rereadpt", disk)
		if err != nil {
			return fmt.Errorf("re-read partition table %s: %s: %w", disk, string(out), err)
		}
		slog.Info("blockdev --rereadpt succeeded", "disk", disk)
	}

	// Brief settle time for the kernel to populate /sys/block/ partition entries
	// after BLKRRPART ioctl. On QEMU/KVM virtio disks the partition scan can be
	// asynchronous and /sys entries may not exist immediately.
	time.Sleep(200 * time.Millisecond)

	// Diagnostic: show device state after partprobe + settle.
	logDeviceState(disk, "after-partprobe")

	// Trigger device node creation for partitions. In initramfs environments
	// without udevd, devtmpfs may not auto-create partition nodes after the
	// kernel re-reads the partition table. mdev -s scans /sys and creates
	// missing device nodes.
	if mdevOut, mdevErr := m.cmd.Run(ctx, "mdev", "-s"); mdevErr != nil {
		slog.Warn("mdev -s failed", "error", mdevErr, "output", string(mdevOut))
	}

	// Diagnostic: show device state after mdev -s.
	logDeviceState(disk, "after-mdev")
	return nil
}

// logDeviceState logs /dev/sd*, /sys/block/ entries, and /proc/partitions
// for debugging partition node creation in initramfs environments.
func logDeviceState(disk, label string) {
	// List /dev/sd* (or /dev/vd* etc) device nodes.
	base := filepath.Dir(disk) // /dev
	prefix := filepath.Base(disk)
	devNodes := listGlob(filepath.Join(base, prefix+"*"))
	slog.Info("device state", "label", label, "dev_nodes", devNodes)

	// List /sys/block/<disk>/ partition entries.
	sysBase := filepath.Base(disk)
	sysDir := "/sys/block/" + sysBase
	sysEntries := listDir(sysDir)
	slog.Info("device state", "label", label, "sys_entries", sysEntries)

	// Read /proc/partitions for the disk.
	if data, err := os.ReadFile("/proc/partitions"); err != nil {
		slog.Warn("device state: cannot read /proc/partitions", "label", label, "error", err)
	} else {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		var relevant []string
		for _, line := range lines {
			if strings.Contains(line, sysBase) || strings.HasPrefix(line, "major") {
				relevant = append(relevant, strings.TrimSpace(line))
			}
		}
		slog.Info("device state", "label", label, "proc_partitions", relevant)
	}
}

func listGlob(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	return matches
}

func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// supportedFilesystems lists filesystem types to try when mounting, in priority order.
var supportedFilesystems = []string{"ext4", "xfs", "btrfs", "ext3", "ext2", "vfat"}

// MountPartition mounts a device at the given mountpoint.
// Tries all supported filesystem types in order: ext4, xfs, btrfs, ext3, ext2, vfat.
// Waits up to 10 seconds for the device node to appear (devtmpfs may lag after partprobe).
func (m *Manager) MountPartition(ctx context.Context, device, mountpoint string) error {
	slog.Info("mounting partition", "device", device, "mountpoint", mountpoint)

	// Wait for the device node to appear — devtmpfs can lag after partprobe.
	if err := waitForDevice(ctx, device); err != nil {
		return fmt.Errorf("device %s not available: %w", device, err)
	}

	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("creating mountpoint %s: %w", mountpoint, err)
	}
	var errs []string
	for _, fsType := range supportedFilesystems {
		err := syscall.Mount(device, mountpoint, fsType, 0, "")
		if err == nil {
			slog.Info("mounted partition", "device", device, "fsType", fsType)
			return nil
		}
		errs = append(errs, fmt.Sprintf("%s=%v", fsType, err))
	}
	return fmt.Errorf("mounting %s at %s: tried %s", device, mountpoint, strings.Join(errs, ", "))
}

// waitForDevice polls for a device node to appear, retrying up to 10 seconds.
// On each iteration, runs mdev -s to trigger device node creation in initramfs
// environments without udevd, where the kernel may update /sys/block/ partition
// entries asynchronously after BLKRRPART.
// Respects context cancellation for clean shutdown.
func waitForDevice(ctx context.Context, device string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for i := range 20 {
		if _, err := os.Stat(device); err == nil {
			slog.Info("device appeared", "device", device, "iteration", i)
			return nil
		}
		// Trigger device node creation from /sys entries that may have appeared
		// since the last poll. mdev -s is fast (~10ms) and idempotent.
		//nolint:gosec // mdev is a fixed busybox command, no user input
		if err := exec.CommandContext(ctx, "mdev", "-s").Run(); err != nil {
			slog.Debug("mdev -s failed during device wait", "device", device, "error", err)
		}
		if _, err := os.Stat(device); err == nil {
			slog.Info("device appeared after mdev", "device", device, "iteration", i)
			return nil
		}
		if i == 0 || i == 9 || i == 19 {
			// Log device state on first, middle, and last iteration.
			disk := strings.TrimRight(device, "0123456789")
			logDeviceState(disk, fmt.Sprintf("waitForDevice-iter%d", i))
		}
		slog.Debug("waiting for device node", "device", device, "iteration", i)
		select {
		case <-ctx.Done():
			return fmt.Errorf("device %s wait canceled: %w", device, ctx.Err())
		case <-ticker.C:
		}
	}
	return fmt.Errorf("device %s did not appear after 10s", device)
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

// exitCodeFromError extracts the process exit code from an error chain.
// Returns -1 if no exit code is found.
func exitCodeFromError(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// CheckFilesystem runs e2fsck on the device.
// Exit codes 0 (clean) and 1 (errors corrected) are acceptable.
// Exit code >= 4 indicates uncorrectable errors and returns an error.
func (m *Manager) CheckFilesystem(ctx context.Context, device string) error {
	slog.Info("checking filesystem", "device", device)
	fsType, fsErr := m.fsType(ctx, device)
	if fsErr != nil {
		slog.Debug("failed to detect filesystem type, falling back to ext check", "device", device, "error", fsErr)
	}

	switch fsType {
	case "xfs":
		out, err := m.cmd.Run(ctx, "xfs_repair", "-n", device)
		if err != nil {
			return fmt.Errorf("xfs_repair check failed on %s: %s: %w", device, strings.TrimSpace(string(out)), err)
		}
		return nil
	case "btrfs":
		out, err := m.cmd.Run(ctx, "btrfs", "check", "--readonly", device)
		if err != nil {
			return fmt.Errorf("btrfs check failed on %s: %s: %w", device, strings.TrimSpace(string(out)), err)
		}
		return nil
	}

	out, err := m.cmd.Run(ctx, "e2fsck", "-fy", device)
	if err != nil {
		exitCode := exitCodeFromError(err)
		if exitCode >= 4 {
			return fmt.Errorf("e2fsck: uncorrectable filesystem errors on %s (exit %d): %w", device, exitCode, err)
		}
		slog.Warn("e2fsck returned non-zero (errors corrected)", "device", device, "exit_code", exitCode, "output", string(out))
	}
	return nil
}

func (m *Manager) fsType(ctx context.Context, device string) (string, error) {
	out, err := m.cmd.Run(ctx, "blkid", "-o", "value", "-s", "TYPE", device)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(string(out))), nil
}

// DisableLVM deactivates LVM volume groups before disk wipe.
func (m *Manager) DisableLVM(ctx context.Context) error {
	slog.Info("deactivating LVM volume groups")
	out, err := m.cmd.Run(ctx, "lvm", "vgchange", "-an")
	if err != nil {
		// Not fatal — LVM may not be present.
		slog.Debug("lvm deactivate (may be expected if no LVM)", "output", string(out), "error", err)
	}
	return nil
}

// EnableLVM activates LVM volume groups.
func (m *Manager) EnableLVM(ctx context.Context) error {
	slog.Info("activating LVM volume groups")
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
		// If chroot binary itself is missing, fall back to syscall-based chroot.
		if isExecNotFound(err) {
			slog.Info("chroot binary not found, using syscall fallback", "root", root)
			return m.chrootSyscall(ctx, root, command)
		}
		// If /bin/bash is missing inside the chroot target, try /bin/sh.
		if isBashNotFound(err) {
			slog.Info("bash not found in chroot, trying /bin/sh", "root", root)
			return m.cmd.Run(ctx, "chroot", root, "/bin/sh", "-c", command)
		}
		return out, fmt.Errorf("chroot exec in %s: %w", root, err)
	}
	return out, nil
}

// chrootSyscall runs a command using SysProcAttr.Chroot instead of the chroot binary.
func (m *Manager) chrootSyscall(ctx context.Context, root, command string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
	out, err := cmd.CombinedOutput()
	if err != nil {
		raw := strings.TrimSpace(string(out))
		if len(raw) > 512 {
			raw = raw[:512] + "...(truncated)"
		}
		raw = strings.NewReplacer("\n", " ", "\r", " ").Replace(raw)
		if raw != "" {
			return out, fmt.Errorf("chroot syscall in %s: %w [output: %s]", root, err, raw)
		}
		return out, fmt.Errorf("chroot syscall in %s: %w", root, err)
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

// isBashNotFound checks whether the error indicates /bin/bash was not found
// inside a chroot target (exit status 127 with "No such file or directory").
func isBashNotFound(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "exit status 127") &&
		strings.Contains(msg, "No such file or directory")
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
			slog.Debug("cannot create efivarfs mount target", "path", efiDst, "error", err)
		} else if err := m.BindMount(efiSrc, efiDst); err != nil {
			slog.Debug("cannot bind-mount efivarfs into chroot", "error", err)
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
		slog.Debug("chroot efivarfs unmount skipped", "path", efiPath, "error", err)
	}
	for _, rel := range []string{"run", "sys", "proc", "dev"} {
		if err := m.Unmount(root + "/" + rel); err != nil {
			slog.Warn("chroot unmount failed", "path", root+"/"+rel, "error", err)
		}
	}
}
