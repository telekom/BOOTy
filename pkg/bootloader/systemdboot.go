//go:build linux

package bootloader

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// SystemdBoot manages systemd-boot bootloader.
type SystemdBoot struct{}

// Install installs systemd-boot into the ESP at rootPath.
func (s *SystemdBoot) Install(rootPath, _ string) error {
	slog.Info("installing systemd-boot", "root", rootPath)
	out, err := exec.CommandContext(context.Background(), "chroot", rootPath, "bootctl", "install").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bootctl install: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Configure is currently a no-op for systemd-boot (uses Type #1 BLS entries).
func (s *SystemdBoot) Configure(_ string, _ BootConfig) error {
	return nil
}

// ListEntries enumerates boot entries via bootctl.
func (s *SystemdBoot) ListEntries(rootPath string) ([]BootEntry, error) {
	out, err := exec.CommandContext(context.Background(), "chroot", rootPath, "bootctl", "list", "--no-pager").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bootctl list: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return parseBootctlOutput(string(out)), nil
}

// parseBootctlOutput parses the text output of bootctl list into BootEntry
// structs. Each entry block starts with a "title:" line.
func parseBootctlOutput(output string) []BootEntry {
	var entries []BootEntry
	var current BootEntry
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "title:"):
			if current.Title != "" {
				entries = append(entries, current)
			}
			current = BootEntry{Title: strings.TrimSpace(strings.TrimPrefix(line, "title:"))}
		case strings.HasPrefix(line, "linux:"):
			current.Kernel = strings.TrimSpace(strings.TrimPrefix(line, "linux:"))
		case strings.HasPrefix(line, "initrd:"):
			current.Initrd = strings.TrimSpace(strings.TrimPrefix(line, "initrd:"))
		case strings.HasPrefix(line, "options:"):
			current.Args = strings.TrimSpace(strings.TrimPrefix(line, "options:"))
		}
	}
	if current.Title != "" {
		entries = append(entries, current)
	}
	return entries
}

// SetDefault sets the default boot entry via bootctl.
func (s *SystemdBoot) SetDefault(rootPath, title string) error {
	out, err := exec.CommandContext(context.Background(), "chroot", rootPath,
		"bootctl", "set-default", title).CombinedOutput() //nolint:gosec // trusted config
	if err != nil {
		return fmt.Errorf("bootctl set-default: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
