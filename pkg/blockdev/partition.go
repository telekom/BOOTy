//go:build linux

// Package blockdev contains Linux block-device path helpers shared by disk and
// image provisioning code.
package blockdev

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PartitionDevicePath returns the device path for a specific partition number.
func PartitionDevicePath(device string, num int) string {
	if strings.HasPrefix(device, "/dev/disk/by-") {
		return fmt.Sprintf("%s-part%d", device, num)
	}

	devName := filepath.Base(device)
	if needsPartitionSeparator(devName) {
		return fmt.Sprintf("%sp%d", device, num)
	}
	return fmt.Sprintf("%s%d", device, num)
}

func needsPartitionSeparator(devName string) bool {
	return strings.HasPrefix(devName, "nvme") ||
		strings.HasPrefix(devName, "loop") ||
		strings.HasPrefix(devName, "mmcblk") ||
		strings.HasPrefix(devName, "md") ||
		strings.HasPrefix(devName, "nbd")
}
