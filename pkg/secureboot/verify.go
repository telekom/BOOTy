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
	// Valid requires SecureBoot enabled, not in setup mode, and all components present.
	result.Valid = result.SecureBootEnabled && !result.SetupMode && cv.allComponentsPresent(result.Components)
	return result, nil
}

// checkComponentPresence checks that expected Secure Boot chain components exist.
// NOTE: This is a presence check only — it does not verify cryptographic signatures.
// Full PE/COFF signature verification is planned but not yet implemented.
func (cv *ChainVerifier) checkComponentPresence() []ComponentStatus {
	paths := []struct {
		name string
		path string
	}{
		{"shim", "/boot/efi/EFI/BOOT/BOOTX64.EFI"},
		{"grub", "/boot/efi/EFI/ubuntu/grubx64.efi"},
		{"kernel", "/boot/vmlinuz"},
	}
	components := make([]ComponentStatus, 0, len(paths))
	for _, p := range paths {
		status := ComponentStatus{Name: p.name}
		if _, err := os.Stat(p.path); err != nil {
			status.Error = fmt.Sprintf("not found: %s", p.path)
		}
		// Signed/Trusted remain false — presence alone does not imply trust.
		// Cryptographic signature verification is not yet implemented.
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
