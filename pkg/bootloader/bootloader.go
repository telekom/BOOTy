// Package bootloader provides a unified interface for bootloader management.
package bootloader

import "context"

// BootConfig holds kernel parameters for bootloader configuration.
type BootConfig struct {
	KernelPath   string
	InitrdPath   string
	Cmdline      string
	DefaultEntry string
}

// BootEntry describes a bootloader menu entry.
type BootEntry struct {
	Title  string
	Kernel string
	Initrd string
	Args   string
}

// Bootloader defines operations for managing a bootloader.
type Bootloader interface {
	// Install sets up the bootloader on the target disk.
	Install(ctx context.Context, rootPath, diskDevice string) error
	// Configure sets kernel parameters and default entry.
	Configure(ctx context.Context, rootPath string, cfg BootConfig) error
	// ListEntries returns the available boot entries.
	ListEntries(ctx context.Context, rootPath string) ([]BootEntry, error)
	// SetDefault sets the default boot entry by title.
	SetDefault(ctx context.Context, rootPath, title string) error
}
