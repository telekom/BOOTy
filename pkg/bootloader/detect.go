//go:build linux

package bootloader

import (
	"os"
	"path/filepath"
	"runtime"
)

// DetectBootloader examines rootPath and returns the appropriate Bootloader.
// It checks for the systemd-boot binary specifically; BOOTX64.EFI is not used
// because on most systems it is the shim loader (Secure Boot), not systemd-boot.
func DetectBootloader(rootPath string) Bootloader {
	name := "systemd-bootx64.efi"
	if runtime.GOARCH == "arm64" {
		name = "systemd-bootaa64.efi"
	}
	sdBoot := filepath.Join(rootPath, "boot", "efi", "EFI", "systemd", name)
	if stat, err := os.Stat(sdBoot); err == nil && !stat.IsDir() {
		return &SystemdBoot{}
	}
	return &GRUB{}
}
