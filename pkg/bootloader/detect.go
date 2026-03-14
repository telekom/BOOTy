package bootloader

import (
	"os"
	"path/filepath"
)

// DetectBootloader auto-detects which bootloader is installed in the
// provisioned OS image by checking for known filesystem markers.
func DetectBootloader(rootPath string) string {
	// systemd-boot: check for the EFI stub binary
	sdBootPaths := []string{
		filepath.Join(rootPath, "usr", "lib", "systemd", "boot", "efi", "systemd-bootx64.efi"),
		filepath.Join(rootPath, "usr", "lib", "systemd", "boot", "efi", "systemd-bootaa64.efi"),
	}
	for _, p := range sdBootPaths {
		if _, err := os.Stat(p); err == nil {
			return "systemd-boot"
		}
	}

	// GRUB: check for grub-install or grub2-install (RHEL/CentOS)
	grubPaths := []string{
		filepath.Join(rootPath, "usr", "sbin", "grub-install"),
		filepath.Join(rootPath, "usr", "sbin", "grub2-install"),
	}
	for _, p := range grubPaths {
		if _, err := os.Stat(p); err == nil {
			return "grub"
		}
	}

	return "unknown"
}
