//go:build linux

package tpm

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var (
	// basePaths are overridable for testing.
	tpmDevicePath   = "/dev/tpm0"
	tpmrmDevicePath = "/dev/tpmrm0"
	sysTPMPath      = "/sys/class/tpm/tpm0"
)

// Info holds TPM device information detected from sysfs.
type Info struct {
	Present       bool   `json:"present"`
	Version       string `json:"version,omitempty"`
	Manufacturer  string `json:"manufacturer,omitempty"`
	FirmwareVer   string `json:"firmwareVer,omitempty"`
	DevicePresent bool   `json:"devicePresent,omitempty"`
	RMPresent     bool   `json:"rmPresent,omitempty"`
}

// Detect checks for TPM 2.0 presence and reads basic device information
// from sysfs without opening the device or requiring external libraries.
func Detect() Info {
	info := Info{}

	if fi, err := os.Stat(tpmDevicePath); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		info.DevicePresent = true
		info.Present = true
	}
	if fi, err := os.Stat(tpmrmDevicePath); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		info.RMPresent = true
		info.Present = true
	}

	if !info.Present {
		return info
	}

	info.Version = readSysfs(filepath.Join(sysTPMPath, "tpm_version_major"))
	if info.Version == "2" {
		info.Version = "2.0"
	}

	info.Manufacturer = readSysfs(filepath.Join(sysTPMPath, "device", "description"))
	if info.Manufacturer == "" {
		info.Manufacturer = readSysfs(filepath.Join(sysTPMPath, "device", "manufacturer"))
	}

	info.FirmwareVer = readSysfs(filepath.Join(sysTPMPath, "device", "firmware_node", "description"))

	slog.Info("TPM detected",
		"version", info.Version,
		"manufacturer", info.Manufacturer,
		"device", info.DevicePresent,
		"rm", info.RMPresent,
	)

	return info
}

// ReadPCRs reads PCR values from the sysfs interface.
func ReadPCRs() (map[int]string, error) {
	pcrDir := filepath.Join(sysTPMPath, "pcr-sha256")
	entries, err := os.ReadDir(pcrDir)
	if err != nil {
		return nil, fmt.Errorf("reading PCR directory: %w", err)
	}

	pcrs := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var idx int
		if _, err := fmt.Sscanf(e.Name(), "%d", &idx); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pcrDir, e.Name()))
		if err != nil {
			slog.Warn("Failed to read PCR", "index", idx, "error", err)
			continue
		}
		// sysfs PCR values are already ASCII hex with a trailing newline.
		pcrs[idx] = strings.TrimSpace(string(data))
	}
	return pcrs, nil
}

func readSysfs(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
