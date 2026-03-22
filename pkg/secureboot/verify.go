package secureboot

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/telekom/BOOTy/pkg/efi"
)

// ChainVerifier validates the Secure Boot chain using EFI variables.
type ChainVerifier struct {
	vars *efi.EFIVarReader
}

// NewChainVerifier creates a chain verifier with the given EFI variable reader.
func NewChainVerifier(vars *efi.EFIVarReader) *ChainVerifier {
	return &ChainVerifier{vars: vars}
}

// Verify checks the Secure Boot chain and returns a result.
func (cv *ChainVerifier) Verify() (*ChainResult, error) {
	result := &ChainResult{}

	enabled, err := cv.vars.IsSecureBootEnabled()
	if err != nil {
		slog.Warn("cannot determine secure boot status", "error", err)
	} else {
		result.SecureBootEnabled = enabled
	}

	setupMode, err := cv.vars.IsSetupMode()
	if err != nil {
		slog.Warn("cannot determine setup mode", "error", err)
	} else {
		result.SetupMode = setupMode
	}

	result.Components = cv.checkComponentPresence()
	// Valid requires SecureBoot enabled, not in setup mode, and all required
	// components present on disk. NOTE: this does NOT verify cryptographic
	// signatures — it only confirms expected files exist. Full PE/COFF
	// signature verification is planned but not yet implemented.
	result.Valid = result.SecureBootEnabled && !result.SetupMode && cv.allComponentsPresent(result.Components)
	return result, nil
}

// checkComponentPresence checks that expected Secure Boot chain components exist.
// NOTE: This is a presence check only — it does not verify cryptographic signatures.
// Full PE/COFF signature verification is planned but not yet implemented.
// checkComponentPresence checks whether boot chain binaries exist on disk.
// This is intentionally a presence-only check — actual signature verification
// would require parsing PE/COFF and validating against db/dbx EFI variables.
func (cv *ChainVerifier) checkComponentPresence() []ComponentStatus {
	paths := []struct {
		name  string
		paths []string
	}{
		{"shim", []string{
			"/boot/efi/EFI/BOOT/BOOTX64.EFI",
			"/boot/efi/EFI/BOOT/BOOTAA64.EFI",
		}},
		{"grub", []string{
			"/boot/efi/EFI/ubuntu/grubx64.efi",
			"/boot/efi/EFI/centos/grubx64.efi",
			"/boot/efi/EFI/redhat/grubx64.efi",
			"/boot/efi/EFI/fedora/grubx64.efi",
			"/boot/efi/EFI/sles/grubx64.efi",
			"/boot/efi/EFI/debian/grubx64.efi",
		}},
		{"kernel", []string{
			"/boot/vmlinuz",
			"/boot/vmlinuz-linux",
		}},
	}
	components := make([]ComponentStatus, 0, len(paths))
	for _, p := range paths {
		status := ComponentStatus{Name: p.name}
		found := false
		for _, path := range p.paths {
			if _, err := os.Stat(path); err == nil {
				found = true
				break
			}
		}
		if !found {
			status.Error = fmt.Sprintf("not found: tried %v", p.paths)
		}
		components = append(components, status)
	}
	return components
}

func (cv *ChainVerifier) allComponentsPresent(components []ComponentStatus) bool {
	for _, c := range components {
		if c.Error != "" {
			return false
		}
	}
	return len(components) > 0
}
