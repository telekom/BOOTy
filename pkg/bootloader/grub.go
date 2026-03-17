//go:build linux

package bootloader

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/telekom/BOOTy/pkg/grubcfg"
)

// GRUB manages GRUB2 bootloader installation and configuration.
type GRUB struct{}

// Install installs GRUB onto diskDevice targeting rootPath.
func (g *GRUB) Install(rootPath, diskDevice string) error {
	slog.Info("installing grub", "root", rootPath, "disk", diskDevice)

	out, err := exec.Command("chroot", rootPath, "grub-install", diskDevice).CombinedOutput() //nolint:gosec // trusted disk path
	if err != nil {
		return fmt.Errorf("grub-install: %s: %w", strings.TrimSpace(string(out)), err)
	}

	out, err = exec.Command("chroot", rootPath, "update-grub").CombinedOutput() //nolint:gosec // trusted root path
	if err != nil {
		return fmt.Errorf("update-grub: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Configure sets the default boot entry and kernel command line.
func (g *GRUB) Configure(rootPath string, cfg BootConfig) error {
	if cfg.DefaultEntry != "" {
		out, err := exec.Command("chroot", rootPath,
			"grub-set-default", cfg.DefaultEntry).CombinedOutput() //nolint:gosec // trusted config
		if err != nil {
			return fmt.Errorf("grub-set-default: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

// ListEntries returns parsed GRUB menu entries from grub.cfg.
func (g *GRUB) ListEntries(rootPath string) ([]BootEntry, error) {
	path := rootPath + "/boot/grub/grub.cfg"
	entries, err := grubcfg.ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("listing grub entries: %w", err)
	}
	var result []BootEntry
	for _, e := range entries {
		result = append(result, BootEntry{
			Title: e.Title, Kernel: e.Linux, Initrd: e.Initrd, Args: e.Args,
		})
	}
	return result, nil
}

// SetDefault sets a GRUB default entry by title.
func (g *GRUB) SetDefault(rootPath, title string) error {
	out, err := exec.Command("chroot", rootPath,
		"grub-set-default", title).CombinedOutput() //nolint:gosec // trusted title
	if err != nil {
		return fmt.Errorf("grub-set-default: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
