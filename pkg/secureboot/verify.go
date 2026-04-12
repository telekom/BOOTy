package secureboot

import (
	"debug/pe"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

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
	// PreconditionsMet requires SecureBoot enabled, not in setup mode, and all required
	// components present on disk. NOTE: this does NOT verify cryptographic
	// signatures — it only confirms expected files exist. Full PE/COFF
	// signature verification is planned but not yet implemented.
	result.PreconditionsMet = result.SecureBootEnabled && !result.SetupMode && cv.allComponentsPresent(result.Components)
	return result, nil
}

// checkComponentPresence checks whether boot chain binaries exist on disk
// and validates PE/COFF headers for EFI binaries.
func (cv *ChainVerifier) checkComponentPresence() []ComponentStatus {
	specs := []struct {
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
	components := make([]ComponentStatus, 0, len(specs))
	for _, s := range specs {
		components = append(components, findValidCandidate(s.name, s.paths))
	}
	return components
}

// findValidCandidate scans candidates in order, returning the first that exists
// and passes PE/COFF validation (for .efi paths). If no candidate passes,
// the returned ComponentStatus carries an error string that distinguishes
// "invalid PE/COFF header" (files found but corrupt) from "not found".
func findValidCandidate(name string, candidates []string) ComponentStatus {
	status := ComponentStatus{Name: name}
	var lastValidationErr error
	anyFound := false
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			// Distinguish between "not exists" (expected) and real IO/permission
			// errors which should fail fast and be reported to the caller.
			if os.IsNotExist(err) {
				continue
			}
			status.Error = fmt.Sprintf("stat %s: %v", path, err)
			return status
		}
		anyFound = true
		if isEFIPath(path) {
			if err := validatePEHeader(path); err != nil {
				slog.Warn("pe/coff validation failed, trying next candidate",
					"path", path, "error", err)
				lastValidationErr = err
				continue
			}
		}
		return status
	}
	if anyFound && lastValidationErr != nil {
		status.Error = fmt.Sprintf("pe/coff validation failed for all candidates %v: %v", candidates, lastValidationErr)
	} else {
		status.Error = fmt.Sprintf("not found: tried %v", candidates)
	}
	return status
}

// isEFIPath reports whether path points to a PE/COFF EFI binary.
// Kernel vmlinuz paths are excluded — they are not PE binaries.
func isEFIPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".efi")
}

// validatePEHeader opens path as a PE/COFF binary using debug/pe and
// returns an error if the file is missing, truncated, has an invalid header,
// or has a machine type that does not match the host architecture.
func validatePEHeader(path string) error {
	f, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("pe/coff parse failed: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			slog.Debug("pe/coff close failed", "path", path, "error", cerr)
		}
	}()

	if err := validatePEMachineType(f); err != nil {
		return err
	}
	return nil
}

// validatePEMachineType returns an error if the PE machine type does not match
// the host architecture. Only amd64 and arm64 are validated; unknown host
// architectures are accepted without error to avoid false negatives.
func validatePEMachineType(f *pe.File) error {
	var wantMachine uint16
	switch runtime.GOARCH {
	case "amd64":
		wantMachine = pe.IMAGE_FILE_MACHINE_AMD64
	case "arm64":
		wantMachine = pe.IMAGE_FILE_MACHINE_ARM64
	default:
		// Unknown host arch — skip machine type check.
		return nil
	}

	got := f.FileHeader.Machine
	if got != wantMachine {
		return fmt.Errorf("pe/coff machine type 0x%04x does not match host arch %s (want 0x%04x)",
			got, runtime.GOARCH, wantMachine)
	}
	return nil
}

func (cv *ChainVerifier) allComponentsPresent(components []ComponentStatus) bool {
	for _, c := range components {
		if c.Error != "" {
			return false
		}
	}
	return len(components) > 0
}
