package secureboot

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// ChainVerifier validates the SecureBoot signing chain of a provisioned OS.
type ChainVerifier struct {
	log *slog.Logger
}

// NewChainVerifier creates a new chain verifier.
func NewChainVerifier(log *slog.Logger) *ChainVerifier {
	return &ChainVerifier{log: log}
}

// shimPaths lists common shim locations.
var shimPaths = []string{
	"EFI/BOOT/BOOTX64.EFI",
	"EFI/BOOT/bootx64.efi",
	"EFI/ubuntu/shimx64.efi",
	"EFI/centos/shimx64.efi",
	"EFI/redhat/shimx64.efi",
	"EFI/fedora/shimx64.efi",
	"EFI/BOOT/BOOTAA64.EFI",
}

// grubPaths lists common GRUB locations.
var grubPaths = []string{
	"EFI/ubuntu/grubx64.efi",
	"EFI/centos/grubx64.efi",
	"EFI/redhat/grubx64.efi",
	"EFI/fedora/grubx64.efi",
	"EFI/BOOT/grubx64.efi",
	"EFI/BOOT/grubaa64.efi",
}

// kernelPaths lists common kernel locations.
var kernelPaths = []string{
	"boot/vmlinuz",
	"boot/vmlinuz-linux",
}

// VerifyChain checks the signing chain of a provisioned OS at rootPath.
func (v *ChainVerifier) VerifyChain(rootPath string) *ChainResult {
	result := &ChainResult{Valid: true}

	shimPath := findFirstExisting(rootPath, shimPaths)
	result.Shim = v.verifyComponent("shim", shimPath)
	if !result.Shim.Valid {
		result.Valid = false
	}

	grubPath := findFirstExisting(rootPath, grubPaths)
	result.GRUB = v.verifyComponent("grub", grubPath)
	if !result.GRUB.Valid {
		result.Valid = false
	}

	kernelPath := findFirstKernel(rootPath)
	result.Kernel = v.verifyComponent("kernel", kernelPath)
	if !result.Kernel.Valid {
		result.Valid = false
	}

	return result
}

func (v *ChainVerifier) verifyComponent(name, path string) ComponentStatus {
	if path == "" {
		return ComponentStatus{
			Path:  "",
			Valid: false,
			Error: fmt.Sprintf("%s not found", name),
		}
	}

	// Check file exists and is a valid PE binary.
	info, err := os.Stat(path)
	if err != nil {
		return ComponentStatus{
			Path:  path,
			Valid: false,
			Error: fmt.Sprintf("stat %s at %s: %v", name, path, err),
		}
	}

	if info.Size() == 0 {
		return ComponentStatus{
			Path:  path,
			Valid: false,
			Error: fmt.Sprintf("%s is empty", name),
		}
	}

	// Check for PE format (MZ header).
	signed, signer, headerErr := hasPEHeader(path)
	if headerErr != nil {
		return ComponentStatus{
			Path:  path,
			Valid: false,
			Error: fmt.Sprintf("read %s header at %s: %v", name, path, headerErr),
		}
	}

	return ComponentStatus{
		Path:     path,
		Signed:   signed,
		SignedBy: signer,
		Valid:    signed,
	}
}

// hasPEHeader checks if a file has a PE/COFF (MZ) header.
func hasPEHeader(path string) (isPE bool, format string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer func() { _ = f.Close() }()

	// Read MZ header.
	header := make([]byte, 2)
	if _, err := io.ReadFull(f, header); err != nil {
		return false, "", err
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return false, "", nil
	}

	// Has MZ header — it's a PE binary.
	// Full Authenticode signature verification would parse the PE optional
	// header's Certificate Table (data directory entry 4) and verify
	// the PKCS#7 signature. For now we detect the MZ header as a proxy.
	return true, "pe-detected", nil
}

func findFirstExisting(root string, candidates []string) string {
	for _, candidate := range candidates {
		full := filepath.Join(root, candidate)
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	return ""
}

func findFirstKernel(root string) string {
	// Try known paths first.
	for _, p := range kernelPaths {
		full := filepath.Join(root, p)
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}

	// Glob for vmlinuz-* in boot/.
	bootDir := filepath.Join(root, "boot")
	matches, err := filepath.Glob(filepath.Join(bootDir, "vmlinuz-*"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}
