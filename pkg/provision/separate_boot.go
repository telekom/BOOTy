//go:build linux

package provision

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// separateBootMountPoint is where a separate /boot filesystem of the image is
// mounted below the target root.
func separateBootMountPoint() string {
	return filepath.Join(newroot, "boot")
}

// mountSeparateBoot mounts the image /boot filesystem from the target fstab
// when the root filesystem carries no /boot/grub, like provision.sh of the
// CAPRF legacy provisioner. Images such as the Ubuntu 24.04 cloud image keep
// grub.cfg, kernels and initramfs on a separate /boot partition, so
// update-grub and update-initramfs must see it.
func (o *Orchestrator) mountSeparateBoot(ctx context.Context) error {
	mountpoint := separateBootMountPoint()
	if isMountPoint(mountpoint) {
		return nil
	}
	needed, source, err := separateBootSource(newroot)
	if err != nil || !needed {
		return err
	}
	if source == "" {
		o.log.Warn("no /boot/grub on the root filesystem and no /boot entry in fstab")
		return nil
	}
	device := o.resolveFstabSource(ctx, source)
	if device == "" {
		o.log.Warn("cannot resolve the /boot fstab source, keeping /boot on the root filesystem", "source", source)
		return nil
	}
	o.log.Info("mounting separate /boot", "device", device, "mountpoint", mountpoint)
	if err := o.disk.MountPartition(ctx, device, mountpoint); err != nil {
		return fmt.Errorf("mounting separate /boot %s: %w", device, err)
	}
	return nil
}

// separateBootSource reports whether root lacks /boot/grub and returns the
// fstab source of its /boot entry, if any.
func separateBootSource(root string) (needed bool, source string, err error) {
	if _, err := os.Stat(filepath.Join(root, "boot", "grub")); err == nil {
		return false, "", nil
	}
	source, err = fstabSource(filepath.Join(root, "etc", "fstab"), "/boot")
	return true, source, err
}

// fstabSource returns the source field of the first fstab entry for target.
func fstabSource(path, target string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // fstab of the provisioned root
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if fields[1] == target {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return "", nil
}

// resolveFstabSource maps an fstab source (UUID=, PARTUUID=, LABEL= or a
// device path) to a device node with blkid. It returns "" when it cannot.
func (o *Orchestrator) resolveFstabSource(ctx context.Context, source string) string {
	var args []string
	switch {
	case strings.HasPrefix(source, "UUID="):
		args = []string{"-U", strings.TrimPrefix(source, "UUID=")}
	case strings.HasPrefix(source, "PARTUUID="):
		args = []string{"-t", source, "-o", "device"}
	case strings.HasPrefix(source, "LABEL="):
		args = []string{"-L", strings.TrimPrefix(source, "LABEL=")}
	case strings.HasPrefix(source, "/dev/"):
		return source
	default:
		return ""
	}
	out, err := o.disk.Run(ctx, "blkid", args...)
	if err != nil {
		o.log.Warn("blkid failed", "source", source, "error", err)
		return ""
	}
	device := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if !strings.HasPrefix(device, "/dev/") {
		return ""
	}
	return device
}

// unmountSeparateBoot unmounts the separate /boot mounted by mountSeparateBoot.
func (o *Orchestrator) unmountSeparateBoot() error {
	mountpoint := separateBootMountPoint()
	if !isMountPoint(mountpoint) {
		return nil
	}
	if err := o.disk.Unmount(mountpoint); err != nil {
		return fmt.Errorf("unmount separate /boot %s: %w", mountpoint, err)
	}
	return nil
}
