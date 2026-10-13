// Package loopdev waits for the partition nodes of loop devices in the e2e
// tests. `losetup --partscan` returns before the kernel and udev have
// created /dev/loopNpM, and in containers without udev the node is never
// created at all, so a plain stat loop fails now and then.
package loopdev

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var partitionRe = regexp.MustCompile(`^(/dev/loop[0-9]+)p([0-9]+)$`)

// SplitPartition returns the loop device and the partition number of a
// loop partition path such as /dev/loop3p2.
func SplitPartition(path string) (dev string, part int, ok bool) {
	m := partitionRe.FindStringSubmatch(path)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// Attach attaches image to a free loop device with partition scanning and
// waits for partitions 1..parts. It detaches the device again on error.
func Attach(image string, parts int) (string, error) {
	out, err := exec.Command("losetup", "--find", "--show", "--partscan", image).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("losetup %s: %w: %s", image, err, out)
	}
	dev := strings.TrimSpace(string(out))
	if err := WaitPartitions(dev, parts, 30*time.Second); err != nil {
		_ = exec.Command("losetup", "--detach", dev).Run()
		return "", err
	}
	return dev, nil
}

// WaitPartitions waits until dev has block device nodes for partitions
// 1..parts. While a partition is missing in sysfs it asks the kernel to
// re-read the partition table; when sysfs has it but /dev does not (no udev),
// it creates the node from the sysfs major:minor.
func WaitPartitions(dev string, parts int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		missing := missingPartitions(dev, parts)
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("partitions %v of %s did not appear within %s", missing, dev, timeout)
		}
		if _, err := exec.LookPath("partx"); err == nil {
			_ = exec.Command("partx", "--update", dev).Run()
		}
		_ = exec.Command("partprobe", dev).Run()
		if _, err := exec.LookPath("udevadm"); err == nil {
			_ = exec.Command("udevadm", "settle", "--timeout=2").Run()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// missingPartitions returns the partition numbers that the kernel does not
// list in sysfs yet. For the listed ones it makes sure that /dev has a node
// with the sysfs major:minor: without udev the node is missing, and a node
// left from an earlier loop device can point to another partition.
func missingPartitions(dev string, parts int) []int {
	var missing []int
	for i := 1; i <= parts; i++ {
		if err := ensureNode(fmt.Sprintf("%sp%d", dev, i)); err != nil {
			missing = append(missing, i)
		}
	}
	return missing
}

func ensureNode(node string) error {
	data, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(node), "dev"))
	if err != nil {
		return err
	}
	var major, minor uint32
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d:%d", &major, &minor); err != nil {
		return fmt.Errorf("parse %s dev: %w", node, err)
	}
	want := unix.Mkdev(major, minor)
	var st unix.Stat_t
	if err := unix.Stat(node, &st); err == nil {
		if st.Mode&unix.S_IFMT == unix.S_IFBLK && uint64(st.Rdev) == want {
			return usable(node)
		}
		if err := os.Remove(node); err != nil {
			return fmt.Errorf("remove stale %s: %w", node, err)
		}
	}
	if err := unix.Mknod(node, unix.S_IFBLK|0o660, int(want)); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("mknod %s: %w", node, err)
	}
	return usable(node)
}

// usable opens node and reads its size. A partition that the kernel is
// still removing or adding fails here, so the caller tries again.
func usable(node string) error {
	f, err := os.Open(node)
	if err != nil {
		return err
	}
	defer f.Close()
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("size of %s: %w", node, err)
	}
	if size <= 0 {
		return fmt.Errorf("%s has no size", node)
	}
	return nil
}
