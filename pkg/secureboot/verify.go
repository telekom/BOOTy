package secureboot

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/telekom/BOOTy/pkg/efi"
)

// ChainVerifier validates the Secure Boot chain using EFI variables.
type ChainVerifier struct {
	vars          *efi.EFIVarReader
	root          string
	pinnedDigests map[string]string
}

// NewChainVerifier creates a chain verifier with the given EFI variable reader.
func NewChainVerifier(vars *efi.EFIVarReader) *ChainVerifier {
	return &ChainVerifier{vars: vars, root: "/", pinnedDigests: make(map[string]string)}
}

// WithRoot configures the root filesystem where boot artifacts are checked.
func (cv *ChainVerifier) WithRoot(root string) *ChainVerifier {
	cv.root = root
	return cv
}

// WithPinnedDigests configures the verifier to enforce SHA256 digests for components.
// The map should be keyed by component name ("shim", "grub", "kernel").
func (cv *ChainVerifier) WithPinnedDigests(digests map[string]string) *ChainVerifier {
	cv.pinnedDigests = make(map[string]string, len(digests))
	for component, digest := range digests {
		cv.pinnedDigests[component] = digest
	}
	return cv
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
	result.PreconditionsMet = result.SecureBootEnabled && !result.SetupMode && cv.allComponentsPresent(result.Components)
	return result, nil
}

// checkComponentPresence checks whether boot chain binaries exist on disk.
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
		resolved := make([]string, 0, len(s.paths))
		for _, p := range s.paths {
			resolved = append(resolved, cv.absPath(p))
		}
		components = append(components, cv.findValidCandidate(s.name, resolved))
	}
	return components
}

func (cv *ChainVerifier) findValidCandidate(name string, candidates []string) ComponentStatus {
	status := ComponentStatus{Name: name}
	var lastValidationErr error
	anyFound := false
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
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
					"component", name, "path", path, "error", err)
				lastValidationErr = err
				continue
			}
		}

		if expected, ok := cv.pinnedDigests[name]; ok {
			if err := validateDigest(path, expected); err != nil {
				slog.Warn("digest validation failed, trying next candidate",
					"component", name, "path", path, "error", err)
				lastValidationErr = err
				continue
			}
		}

		return status
	}
	if anyFound && lastValidationErr != nil {
		status.Error = fmt.Sprintf("validation failed for all candidates %v: %v", candidates, lastValidationErr)
	} else {
		status.Error = fmt.Sprintf("not found: tried %v", candidates)
	}
	return status
}

func validateDigest(path, expected string) error {
	normalizedExpected, err := normalizeSHA256Digest(expected)
	if err != nil {
		return fmt.Errorf("invalid expected digest: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Debug("close artifact after digest validation failed", "path", path, "error", err)
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash artifact: %w", err)
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if !strings.EqualFold(got, normalizedExpected) {
		return fmt.Errorf("digest mismatch: got sha256:%s, want sha256:%s", got, normalizedExpected)
	}
	return nil
}

func normalizeSHA256Digest(value string) (string, error) {
	digest := strings.TrimSpace(strings.ToLower(value))
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("must be %d hex characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("must be hex characters: %w", err)
	}
	return digest, nil
}

func (cv *ChainVerifier) absPath(path string) string {
	root := cv.root
	if root == "" {
		root = "/"
	}
	if root == "/" {
		return path
	}
	return filepath.Join(root, strings.TrimPrefix(path, "/"))
}

func isEFIPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".efi")
}

func validatePEHeader(path string) (retErr error) {
	f, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("pe/coff parse failed: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			if retErr == nil {
				retErr = fmt.Errorf("pe/coff close: %w", cerr)
			} else {
				slog.Debug("pe/coff close also failed", "path", path, "error", cerr)
			}
		}
	}()

	return validatePEMachineType(f)
}

func validatePEMachineType(f *pe.File) error {
	var wantMachine uint16
	switch runtime.GOARCH {
	case "amd64":
		wantMachine = pe.IMAGE_FILE_MACHINE_AMD64
	case "arm64":
		wantMachine = pe.IMAGE_FILE_MACHINE_ARM64
	default:
		return nil
	}

	got := f.Machine
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
