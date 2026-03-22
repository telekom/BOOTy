// Package efi provides EFI variable access and boot entry construction.
package efi

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const efiVarsDir = "/sys/firmware/efi/efivars"

// EFIVarReader reads and writes EFI variables from efivarfs.
type EFIVarReader struct {
	basePath string
}

// NewEFIVarReader creates an EFI variable reader.
// Use "" for basePath to use the system default (/sys/firmware/efi/efivars).
func NewEFIVarReader(basePath string) *EFIVarReader {
	if basePath == "" {
		basePath = efiVarsDir
	}
	return &EFIVarReader{basePath: basePath}
}

// ReadVar reads the value of an EFI variable (skips the 4-byte attributes prefix).
func (r *EFIVarReader) ReadVar(name string) ([]byte, error) {
	path := filepath.Join(r.basePath, name)
	data, err := os.ReadFile(path) //nolint:gosec // trusted efivarfs path
	if err != nil {
		return nil, fmt.Errorf("reading efi var %s: %w", name, err)
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("efi var %s too short (%d bytes)", name, len(data))
	}
	return data[4:], nil
}

// WriteVar writes an EFI variable with the given attributes and data.
func (r *EFIVarReader) WriteVar(name string, attrs uint32, data []byte) error {
	path := filepath.Join(r.basePath, name)
	buf := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(buf[:4], attrs)
	copy(buf[4:], data)
	if err := os.WriteFile(path, buf, 0o644); err != nil { //nolint:gosec // efivarfs requires 0644
		return fmt.Errorf("writing efi var %s: %w", name, err)
	}
	return nil
}

// ListVars returns all EFI variable names.
func (r *EFIVarReader) ListVars() ([]string, error) {
	entries, err := os.ReadDir(r.basePath)
	if err != nil {
		return nil, fmt.Errorf("listing efi vars: %w", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

// IsSecureBootEnabled checks if SecureBoot is currently enabled.
func (r *EFIVarReader) IsSecureBootEnabled() (bool, error) {
	data, err := r.ReadVar("SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c")
	if err != nil {
		return false, err
	}
	if len(data) < 1 {
		return false, nil
	}
	return data[0] == 1, nil
}

// IsSetupMode checks if the system is in UEFI Setup Mode.
func (r *EFIVarReader) IsSetupMode() (bool, error) {
	data, err := r.ReadVar("SetupMode-8be4df61-93ca-11d2-aa0d-00e098032b8c")
	if err != nil {
		return false, err
	}
	if len(data) < 1 {
		return false, nil
	}
	return data[0] == 1, nil
}

// BootEntry represents an EFI boot entry for NVRAM management.
type BootEntry struct {
	Description  string
	Loader       string
	OptionalData []byte
}

// BuildLoadOption constructs a raw EFI_LOAD_OPTION from a BootEntry.
func BuildLoadOption(entry BootEntry) []byte {
	const efiLoadOptionActive = 0x00000001
	desc := encodeUTF16LE(entry.Description)
	filePath := buildFileDevicePath(entry.Loader)
	optSize := len(entry.OptionalData)
	pathLen := uint16(len(filePath))

	// EFI_LOAD_OPTION: Attributes(4) + FilePathListLength(2) + Description(utf16) + FilePath + OptionalData
	buf := make([]byte, 0, 4+2+len(desc)+len(filePath)+optSize)

	// Attributes
	attrBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(attrBuf, efiLoadOptionActive)
	buf = append(buf, attrBuf...)

	// File path list length
	pathLenBuf := make([]byte, 2)
	binary.LittleEndian.PutUint16(pathLenBuf, pathLen)
	buf = append(buf, pathLenBuf...)

	buf = append(buf, desc...)
	buf = append(buf, filePath...)
	buf = append(buf, entry.OptionalData...)
	return buf
}

// encodeUTF16LE converts a Go string to a null-terminated UTF-16LE byte slice.
func encodeUTF16LE(s string) []byte {
	runes := utf16.Encode([]rune(s + "\x00"))
	buf := make([]byte, len(runes)*2)
	for i, r := range runes {
		binary.LittleEndian.PutUint16(buf[i*2:], r)
	}
	return buf
}

// buildFileDevicePath creates a minimal file device path for an EFI loader.
func buildFileDevicePath(loader string) []byte {
	const (
		mediaType     = 0x04
		filePathType  = 0x04
		endDevicePath = 0x7f
		endSubType    = 0xff
	)

	// Normalize path separators for EFI
	loader = strings.ReplaceAll(loader, "/", "\\")

	pathData := encodeUTF16LE(loader)
	// Device path node: Type(1) + SubType(1) + Length(2) + PathData
	nodeLen := uint16(4 + len(pathData))

	buf := make([]byte, 0, int(nodeLen)+4) // +4 for end device path
	buf = append(buf, mediaType, filePathType)
	nodeLenBuf := make([]byte, 2)
	binary.LittleEndian.PutUint16(nodeLenBuf, nodeLen)
	buf = append(buf, nodeLenBuf...)
	buf = append(buf, pathData...)

	// End of device path
	buf = append(buf, endDevicePath, endSubType, 4, 0)
	return buf
}
