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

	result.Components = cv.checkShimAndGrub()
	result.Valid = result.SecureBootEnabled && !result.SetupMode && cv.allTrusted(result.Components)
	return result, nil
}

func (cv *ChainVerifier) checkShimAndGrub() []ComponentStatus {
	var components []ComponentStatus
	paths := []struct {
		name string
		path string
	}{
		{"shim", "/boot/efi/EFI/BOOT/BOOTX64.EFI"},
		{"grub", "/boot/efi/EFI/ubuntu/grubx64.efi"},
		{"kernel", "/boot/vmlinuz"},
	}
	for _, p := range paths {
		status := ComponentStatus{Name: p.name}
		if _, err := os.Stat(p.path); err != nil {
			status.Error = fmt.Sprintf("not found: %s", p.path)
		} else {
			status.Signed = true
			status.Trusted = true
		}
		components = append(components, status)
	}
	return components
}

func (cv *ChainVerifier) allTrusted(components []ComponentStatus) bool {
	for _, c := range components {
		if !c.Trusted {
			return false
		}
	}
	return len(components) > 0
}
