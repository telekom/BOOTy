//go:build linux

package bootloader

import "os"

// DetectBootloader examines rootPath and returns the appropriate Bootloader.
// It checks for systemd-boot first, then falls back to GRUB.
func DetectBootloader(rootPath string) Bootloader {
	if stat, err := os.Stat(rootPath + "/boot/efi/EFI/systemd/systemd-bootx64.efi"); err == nil && !stat.IsDir() {
		return &SystemdBoot{}
	}
	if stat, err := os.Stat(rootPath + "/boot/efi/EFI/BOOT/BOOTX64.EFI"); err == nil && !stat.IsDir() {
		return &SystemdBoot{}
	}
	return &GRUB{}
}
