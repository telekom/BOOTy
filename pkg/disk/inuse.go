//go:build linux

package disk

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// refuseInUse returns an error when device, or a device on it, backs a
// mounted file system or an active swap area. Every destructive disk
// operation calls it first. A device on top counts through the slaves of
// dm and MD devices (LVM, dm-crypt, RAID), and a partition makes its disk
// busy. So the running root and the disks of a host that runs BOOTy in a
// container are never wiped.
//
// The check reads procRoot, which NewManager sets only for a manager that
// runs real commands; a manager with a test commander writes nothing.
func (m *Manager) refuseInUse(device string) error {
	if m.procRoot == "" {
		return nil
	}
	busy, err := m.busyDevices()
	if err != nil {
		return fmt.Errorf("refusing to wipe %s: cannot check whether it is in use: %w", device, err)
	}
	name := blockName(device)
	if reason, ok := busy[name]; ok {
		return fmt.Errorf("refusing to wipe %s: it backs %s", device, reason)
	}
	return nil
}

// blockName returns the kernel name of a /dev path, following symlinks
// such as /dev/md/name or /dev/mapper/name.
func blockName(device string) string {
	if resolved, err := filepath.EvalSymlinks(device); err == nil {
		device = resolved
	}
	return filepath.Base(device)
}

// busyDevices maps the kernel name of each block device that backs a
// mounted file system or an active swap area to the reason.
func (m *Manager) busyDevices() (map[string]string, error) {
	busy := map[string]string{}
	mountinfo, err := os.ReadFile(filepath.Join(m.procRoot, "self", "mountinfo"))
	if err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(mountinfo))
	for scanner.Scan() {
		// 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		link, err := os.Readlink(filepath.Join(m.sysfsRoot, "dev", "block", fields[2]))
		if err != nil {
			continue // not a block device (tmpfs, proc, overlay, ...)
		}
		m.markBusy(busy, filepath.Base(link), "the file system mounted at "+fields[4])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan mountinfo: %w", err)
	}
	swaps, err := os.ReadFile(filepath.Join(m.procRoot, "swaps"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read swaps: %w", err)
	}
	for i, line := range strings.Split(string(swaps), "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) == 0 || !strings.HasPrefix(fields[0], "/dev/") {
			continue // header, or a swap file on a mounted file system
		}
		m.markBusy(busy, blockName(fields[0]), "the active swap "+fields[0])
	}
	return busy, nil
}

// markBusy marks name, the devices below it and, for a partition, its disk.
func (m *Manager) markBusy(busy map[string]string, name, reason string) {
	if _, done := busy[name]; done || name == "" {
		return
	}
	busy[name] = reason
	dir := filepath.Join(m.sysfsRoot, "class", "block", name)
	if slaves, err := os.ReadDir(filepath.Join(dir, "slaves")); err == nil {
		for _, s := range slaves {
			m.markBusy(busy, s.Name(), reason)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "partition")); err == nil { //nolint:gosec // sysfs path from kernel device names
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			m.markBusy(busy, filepath.Base(filepath.Dir(resolved)), reason)
		}
	}
}
