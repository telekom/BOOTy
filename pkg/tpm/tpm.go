package tpm

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	sysfsTPMBase = "/sys/class/tpm"
	devTPMRM     = "/dev/tpmrm0"
	devTPM       = "/dev/tpm0"
)

// Info holds TPM metadata from sysfs.
type Info struct {
	Present      bool              `json:"present"`
	Version      string            `json:"version"`
	Manufacturer string            `json:"manufacturer"`
	Firmware     string            `json:"firmware,omitempty"`
	DevicePath   string            `json:"devicePath,omitempty"`
	PCRs         map[int][]byte    `json:"pcrs,omitempty"`
}

// Detect checks for TPM presence via sysfs and reads basic metadata.
func Detect() *Info {
	info := &Info{}
	base := filepath.Join(sysfsTPMBase, "tpm0")

	if _, err := os.Stat(base); err != nil {
		return info
	}
	info.Present = true

	if v, err := readSysfsFile(filepath.Join(base, "tpm_version_major")); err == nil {
		info.Version = fmt.Sprintf("%s.0", strings.TrimSpace(v))
	}
	if m, err := readSysfsFile(filepath.Join(base, "device", "vendor")); err == nil {
		info.Manufacturer = strings.TrimSpace(m)
	}
	if fw, err := readSysfsFile(filepath.Join(base, "device", "firmware_node", "description")); err == nil {
		info.Firmware = strings.TrimSpace(fw)
	}

	// Prefer tpmrm0 (resource-managed) over tpm0.
	switch {
	case fileExists(devTPMRM):
		info.DevicePath = devTPMRM
	case fileExists(devTPM):
		info.DevicePath = devTPM
	}

	info.PCRs = readSysfsPCRs(base)
	return info
}

// readSysfsPCRs reads PCR values from the sysfs SHA256 bank.
func readSysfsPCRs(base string) map[int][]byte {
	pcrDir := filepath.Join(base, "pcr-sha256")
	entries, err := os.ReadDir(pcrDir)
	if err != nil {
		return nil
	}
	pcrs := make(map[int][]byte, len(entries))
	for _, entry := range entries {
		idx, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pcrDir, entry.Name())) //nolint:gosec // sysfs
		if err != nil {
			continue
		}
		hexStr := strings.TrimSpace(string(data))
		digest, err := hex.DecodeString(hexStr)
		if err != nil {
			continue
		}
		pcrs[idx] = digest
	}
	return pcrs
}

func readSysfsFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // intentional sysfs read
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
