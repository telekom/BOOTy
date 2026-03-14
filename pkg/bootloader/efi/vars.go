package efi

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"unicode/utf16"
)

const (
	// EFIVarsPath is the sysfs path for EFI variables.
	EFIVarsPath = "/sys/firmware/efi/efivars"

	// EFI variable attributes for non-volatile boot entries.
	attrNVBSRT uint32 = 0x07 // NON_VOLATILE | BOOTSERVICE_ACCESS | RUNTIME_ACCESS
)

// BootEntry represents an EFI boot entry.
type BootEntry struct {
	Num        int
	Label      string
	LoaderPath string
	Active     bool
}

// Validate checks that a BootEntry has required fields.
func (e *BootEntry) Validate() error {
	if e.Num < 0 || e.Num > 0xFFFF {
		return fmt.Errorf("boot entry number %d out of range 0-0xFFFF", e.Num)
	}
	if e.Label == "" {
		return fmt.Errorf("boot entry label must not be empty")
	}
	if e.LoaderPath == "" {
		return fmt.Errorf("boot entry loader path must not be empty")
	}
	return nil
}

// VarName returns the EFI variable name for this boot entry.
func (e *BootEntry) VarName() string {
	return fmt.Sprintf("Boot%04X-8be4df61-93ca-11d2-aa0d-00e098032b8c", e.Num)
}

// VarPath returns the full sysfs path for this boot entry.
func (e *BootEntry) VarPath() string {
	return filepath.Join(EFIVarsPath, e.VarName())
}

// BuildLoadOption constructs the raw EFI_LOAD_OPTION binary data for a
// boot entry, including the 4-byte EFI variable attribute prefix.
func BuildLoadOption(entry *BootEntry) ([]byte, error) {
	if err := entry.Validate(); err != nil {
		return nil, fmt.Errorf("invalid boot entry: %w", err)
	}

	// 4-byte EFI variable attributes
	var buf []byte
	buf = binary.LittleEndian.AppendUint32(buf, attrNVBSRT)

	// EFI_LOAD_OPTION.Attributes (LOAD_OPTION_ACTIVE = 0x01)
	var loadAttrs uint32
	if entry.Active {
		loadAttrs = 0x01
	}
	buf = binary.LittleEndian.AppendUint32(buf, loadAttrs)

	// Description as UCS-2 null-terminated
	desc := encodeUCS2(entry.Label)

	// FilePathListLength
	devPath := buildFileDevicePath(entry.LoaderPath)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(devPath))) //nolint:gosec // len fits uint16

	buf = append(buf, desc...)
	buf = append(buf, devPath...)

	return buf, nil
}

// encodeUCS2 encodes a Go string to UCS-2LE with null terminator.
func encodeUCS2(s string) []byte {
	runes := []rune(s)
	utf16Encoded := utf16.Encode(runes)
	buf := make([]byte, (len(utf16Encoded)+1)*2) // +1 for null terminator
	for i, cp := range utf16Encoded {
		binary.LittleEndian.PutUint16(buf[i*2:], cp)
	}
	// Last 2 bytes are already 0x00 0x00 (null terminator)
	return buf
}

// buildFileDevicePath creates a minimal EFI device path for a file.
// Type 0x04 (Media), Sub-Type 0x04 (File Path).
func buildFileDevicePath(loaderPath string) []byte {
	pathUCS2 := encodeUCS2(loaderPath)

	// Device path node: Type(1) + SubType(1) + Length(2) + path data
	nodeLen := 4 + len(pathUCS2)
	node := make([]byte, nodeLen)
	node[0] = 0x04                                           // Media Device Path
	node[1] = 0x04                                           // File Path
	binary.LittleEndian.PutUint16(node[2:], uint16(nodeLen)) //nolint:gosec // fits uint16

	copy(node[4:], pathUCS2)

	// End device path node: Type 0x7F, Sub-Type 0xFF, Length 4
	end := []byte{0x7F, 0xFF, 0x04, 0x00}

	return append(node, end...)
}
