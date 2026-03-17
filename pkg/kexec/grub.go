package kexec

import (
	"fmt"
	"io"

	"github.com/telekom/BOOTy/pkg/grubcfg"
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
	parsedEntries, err := grubcfg.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse grub config: %w", err)
	}

	entries := make([]BootEntry, 0, len(parsedEntries))
	for _, entry := range parsedEntries {
		entries = append(entries, BootEntry{
			Name:       entry.Title,
			Kernel:     entry.Kernel,
			Initramfs:  entry.Initrd,
			KernelArgs: entry.Cmdline,
		})
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
	return grubcfg.ExtractMenuEntryTitle(line)
}
