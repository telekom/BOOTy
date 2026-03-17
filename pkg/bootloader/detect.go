package bootloader

import (
	"os"
	"path/filepath"
)

// DetectBootloader auto-detects which bootloader is installed in the provisioned OS image.
func DetectBootloader(rootPath string) string {
	// Prefer concrete boot artifacts over generic shipped binaries.
	if hasAnyPath(rootPath,
		filepath.Join("boot", "efi", "loader", "loader.conf"),
		filepath.Join("boot", "loader", "loader.conf"),
		filepath.Join("boot", "efi", "EFI", "systemd", "systemd-bootx64.efi"),
		filepath.Join("boot", "efi", "EFI", "systemd", "systemd-bootaa64.efi"),
	) {
		return "systemd-boot"
	}

	if hasAnyPath(rootPath,
		filepath.Join("boot", "grub", "grub.cfg"),
		filepath.Join("boot", "grub2", "grub.cfg"),
	) {
		return "grub"
	}

	// Fallback: binary-based detection.
	if hasAnyPath(rootPath,
		filepath.Join("usr", "sbin", "grub-install"),
		filepath.Join("usr", "sbin", "grub2-install"),
	) {
		return "grub"
	}

	if hasAnyPath(rootPath,
		filepath.Join("usr", "lib", "systemd", "boot", "efi", "systemd-bootx64.efi"),
		filepath.Join("usr", "lib", "systemd", "boot", "efi", "systemd-bootaa64.efi"),
	) {
		return "systemd-boot"
	}

	return "unknown"
}

func hasAnyPath(rootPath string, relPaths ...string) bool {
	for _, rel := range relPaths {
		if _, err := os.Stat(filepath.Join(rootPath, rel)); err == nil {
			return true
		}
	}
	return false
}
