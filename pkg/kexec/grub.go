package kexec

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// BootEntry represents a grub boot entry.
type BootEntry struct {
	Name       string
	Kernel     string // Path to kernel (e.g., /boot/vmlinuz-5.15.0-generic)
	Initramfs  string // Path to initrd (e.g., /boot/initrd.img-5.15.0-generic)
	KernelArgs string // Kernel command line
}

// ParseGrubCfg parses a grub.cfg file and returns all boot entries.
// Handles menuentry, linux/linux16/linuxefi, and initrd/initrd16/initrdefi directives.
func ParseGrubCfg(r io.Reader) ([]BootEntry, error) {
	var entries []BootEntry
	var current *BootEntry
	depth := 0

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "menuentry ") {
			name := extractMenuEntryName(line)
			entry := BootEntry{Name: name}
			current = &entry
			depth = 1
			continue
		}

		if current == nil {
			continue
		}

		if strings.Contains(line, "{") {
			depth++
		}
		if strings.Contains(line, "}") {
			depth--
			if depth <= 0 {
				entries = append(entries, *current)
				current = nil
				depth = 0
			}
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch fields[0] {
		case "linux", "linux16", "linuxefi":
			current.Kernel = fields[1]
			if len(fields) > 2 {
				current.KernelArgs = strings.Join(fields[2:], " ")
			}
		case "initrd", "initrd16", "initrdefi":
			current.Initramfs = fields[1]
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning grub.cfg: %w", err)
	}

	// Handle entry without closing brace.
	if current != nil {
		entries = append(entries, *current)
	}

	return entries, nil
}

// GetDefaultEntry returns the first boot entry (default).
func GetDefaultEntry(entries []BootEntry) (*BootEntry, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("no boot entries found")
	}
	return &entries[0], nil
}

// extractMenuEntryName extracts the name string from a menuentry line.
// Format: menuentry 'Name' ... { or menuentry "Name" ... {.
func extractMenuEntryName(line string) string {
	for _, quote := range []byte{'\'', '"'} {
		start := strings.IndexByte(line, quote)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(line[start+1:], quote)
		if end < 0 {
			continue
		}
		return line[start+1 : start+1+end]
	}
	// Fallback: strip "menuentry " prefix and trailing " {".
	name := strings.TrimPrefix(line, "menuentry ")
	name = strings.TrimSuffix(name, " {")
	return strings.TrimSpace(name)
}
