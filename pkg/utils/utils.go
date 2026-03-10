package utils

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// CmdlinePath is the default location for the cmdline.
const CmdlinePath = "/proc/cmdline"

// ParseCmdLine will read through the command line and return the source and destination.
func ParseCmdLine(cmdlinePath string) (m map[string]string, err error) {
	// allow path override
	if cmdlinePath == "" {
		cmdlinePath = CmdlinePath
	}

	m = make(map[string]string)
	// Read the file
	b, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return
	}

	// Split by whitespace
	entries := strings.Fields(string(b))

	// find k=v entries
	for x := range entries {
		kv := strings.Split(entries[x], "=")
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return
}

// ClearScreen will clear the screen of all text.
func ClearScreen() {
	fmt.Print("\033[2J")
}

// GetBlockDeviceSize will read the size from the /sys/block for a specific block device.
func GetBlockDeviceSize(device string) (int64, error) {

	// This should return the path to the block device and it's size (in sectores)
	// Each sector is 512 bytes

	devPath := fmt.Sprintf("/sys/block/%s/size", device)

	data, err := os.ReadFile(devPath)
	if err != nil {
		return 0, fmt.Errorf("reading block device size: %w", err)
	}
	parsedData := strings.TrimSpace(string(data))
	size, err := strconv.ParseInt(parsedData, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing block device size %q: %w", parsedData, err)
	}
	return size * 512, nil
}

// DashMac makes a mac address something that can be used in a URL.
func DashMac(mac string) string {
	return strings.ReplaceAll(mac, ":", "-")
}

// ClearDir is a helper function to remove all files in a directory.
func ClearDir(dir string) error {
	names, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading directory %q: %w", dir, err)
	}
	var errs []error
	for _, entry := range names {
		if err := os.RemoveAll(path.Join(dir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
