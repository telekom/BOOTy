package secureboot

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultEFIVarDir = "/sys/firmware/efi/efivars"

// EFIVarReader reads EFI variables from efivarfs.
type EFIVarReader struct {
	basePath string
}

// NewEFIVarReader creates a reader for the given efivarfs mount point.
func NewEFIVarReader(basePath string) *EFIVarReader {
	if basePath == "" {
		basePath = defaultEFIVarDir
	}
	return &EFIVarReader{basePath: basePath}
}

// ReadVar reads an EFI variable by name (without GUID suffix).
// Returns the data portion (attribute bytes stripped).
func (r *EFIVarReader) ReadVar(name string) ([]byte, error) {
	matches, err := filepath.Glob(filepath.Join(r.basePath, name+"-*"))
	if err != nil {
		return nil, fmt.Errorf("glob efi variable %s: %w", name, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("efi variable %s not found", name)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, fmt.Errorf("read efi variable %s: %w", name, err)
	}
	if len(data) <= 4 {
		return nil, fmt.Errorf("efi variable %s too short (%d bytes)", name, len(data))
	}
	return data[4:], nil
}

// IsSecureBootEnabled checks if SecureBoot is currently active.
func (r *EFIVarReader) IsSecureBootEnabled() (bool, error) {
	data, err := r.ReadVar("SecureBoot")
	if err != nil {
		return false, fmt.Errorf("read SecureBoot variable: %w", err)
	}
	if len(data) == 0 {
		return false, nil
	}
	return data[0] == 1, nil
}

// IsSetupMode checks if the system is in UEFI setup mode.
func (r *EFIVarReader) IsSetupMode() (bool, error) {
	data, err := r.ReadVar("SetupMode")
	if err != nil {
		return false, fmt.Errorf("read SetupMode variable: %w", err)
	}
	if len(data) == 0 {
		return false, nil
	}
	return data[0] == 1, nil
}

// WriteVar writes an EFI variable with the given attributes and data.
func (r *EFIVarReader) WriteVar(fullName string, attrs uint32, data []byte) error {
	path := filepath.Join(r.basePath, fullName)

	// Remove immutable flag if file exists.
	if _, err := os.Stat(path); err == nil {
		if removeErr := removeImmutable(path); removeErr != nil {
			return fmt.Errorf("remove immutable flag on %s: %w", fullName, removeErr)
		}
	}

	buf := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(buf[:4], attrs)
	copy(buf[4:], data)

	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("write efi variable %s: %w", fullName, err)
	}
	return nil
}

// removeImmutable clears the immutable attribute on an efivarfs file.
// TODO: implement FS_IOC_SETFLAGS ioctl (linux-only) to clear FS_IMMUTABLE_FL.
func removeImmutable(_ string) error {
	return nil
}

// ListVars returns all EFI variable names matching a prefix.
func (r *EFIVarReader) ListVars(prefix string) ([]string, error) {
	entries, err := os.ReadDir(r.basePath)
	if err != nil {
		return nil, fmt.Errorf("read efivarfs directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names, nil
}
