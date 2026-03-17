package bootloader

import "context"

// Bootloader abstracts bootloader installation and configuration.
type Bootloader interface {
	// Name returns the bootloader type ("grub", "systemd-boot").
	Name() string

	// Install installs the bootloader into the provisioned OS.
	// rootPath is the mounted root filesystem of the target OS.
	// espPath is the EFI System Partition mount point on the host filesystem.
	// GRUB implementations may additionally require espPath to be located under
	// rootPath because installation runs in a chroot.
	Install(ctx context.Context, rootPath string, espPath string) error

	// Configure sets default kernel, cmdline, timeout.
	Configure(ctx context.Context, cfg *BootConfig) error

	// ListEntries returns available boot entries.
	ListEntries(ctx context.Context, rootPath string) ([]BootEntry, error)

	// SetDefault sets the default boot entry.
	SetDefault(ctx context.Context, entryID string) error
}

// BootConfig holds bootloader configuration.
type BootConfig struct {
	DefaultEntry  string      `json:"defaultEntry"`
	KernelCmdline string      `json:"kernelCmdline"`
	ExtraParams   string      `json:"extraParams"`
	Timeout       int         `json:"timeout"`
	RootDevice    string      `json:"rootDevice"`
	Entries       []BootEntry `json:"entries,omitempty"`
}

// BootEntry represents a single boot menu entry.
type BootEntry struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kernel    string `json:"kernel"`
	Initrd    string `json:"initrd"`
	Cmdline   string `json:"cmdline"`
	IsDefault bool   `json:"isDefault"`
}
