//go:build linux

package bootloader

import (
	"os"
	"path/filepath"
)

// DetectBootloader examines rootPath and returns the appropriate Bootloader.
// It checks for the systemd-boot binary specifically; BOOTX64.EFI is not used
// because on most systems it is the shim loader (Secure Boot), not systemd-boot.
func DetectBootloader(rootPath string) Bootloader {
	sdBoot := filepath.Join(rootPath, "boot", "efi", "EFI", "systemd", "systemd-bootx64.efi")
	if stat, err := os.Stat(sdBoot); err == nil && !stat.IsDir() {
		return &SystemdBoot{}
	}
	return &GRUB{}
}
