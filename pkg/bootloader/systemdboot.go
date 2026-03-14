//go:build linux

package bootloader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SystemdBoot implements the Bootloader interface for systemd-boot.
type SystemdBoot struct {
	Log      *slog.Logger
	rootPath string
	espPath  string
}

// NewSystemdBoot creates a new systemd-boot bootloader manager.
func NewSystemdBoot(log *slog.Logger) *SystemdBoot {
	return &SystemdBoot{Log: log}
}

// Name returns the bootloader type.
func (s *SystemdBoot) Name() string { return "systemd-boot" }

// Install installs systemd-boot via bootctl.
func (s *SystemdBoot) Install(ctx context.Context, rootPath, espPath string) error {
	s.rootPath = rootPath
	s.espPath = espPath

	cmd := exec.CommandContext(ctx, "bootctl", "install",
		"--esp-path="+espPath,
		"--root="+rootPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bootctl install: %s: %w", string(out), err)
	}
	s.Log.Info("systemd-boot installed", "esp", espPath)
	return nil
}

// Configure generates loader.conf and boot entry files.
func (s *SystemdBoot) Configure(_ context.Context, cfg *BootConfig) error {
	if s.espPath == "" {
		return fmt.Errorf("ESP path not set, call Install first")
	}
	// Generate loader.conf
	if err := s.generateLoaderConf(cfg); err != nil {
		return err
	}

	// Generate boot entries
	for _, entry := range cfg.Entries {
		if err := s.generateEntry(&entry); err != nil {
			return err
		}
	}
	return nil
}

// ListEntries returns available boot entries from the loader entries directory.
func (s *SystemdBoot) ListEntries(_ context.Context, rootPath string) ([]BootEntry, error) {
	entriesDir := filepath.Join(rootPath, "boot", "efi", "loader", "entries")
	if _, err := os.Stat(entriesDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat entries dir: %w", err)
		}
		// Try alternative path
		entriesDir = filepath.Join(rootPath, "boot", "loader", "entries")
	}

	dirEntries, err := os.ReadDir(entriesDir)
	if err != nil {
		return nil, fmt.Errorf("read entries dir: %w", err)
	}

	var entries []BootEntry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".conf") {
			continue
		}
		entry, err := parseLoaderEntry(filepath.Join(entriesDir, de.Name()))
		if err != nil {
			s.Log.Warn("skip entry", "file", de.Name(), "err", err)
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// SetDefault sets the default boot entry via bootctl.
func (s *SystemdBoot) SetDefault(ctx context.Context, entryID string) error {
	cmd := exec.CommandContext(ctx, "bootctl", "set-default", entryID+".conf")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bootctl set-default %s: %s: %w", entryID, string(out), err)
	}
	return nil
}

func (s *SystemdBoot) generateLoaderConf(cfg *BootConfig) error {
	loaderDir := filepath.Join(s.espPath, "loader")
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		return fmt.Errorf("create loader dir: %w", err)
	}
	content := fmt.Sprintf("default %s.conf\ntimeout %d\nconsole-mode max\n",
		cfg.DefaultKernel, cfg.Timeout)
	loaderPath := filepath.Join(loaderDir, "loader.conf")
	if err := os.WriteFile(loaderPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write loader.conf: %w", err)
	}
	return nil
}

func (s *SystemdBoot) generateEntry(entry *BootEntry) error {
	// Sanitize entry ID to prevent path traversal.
	safeID := filepath.Base(entry.ID)
	if safeID != entry.ID || safeID == "." || safeID == ".." {
		return fmt.Errorf("invalid entry ID: %q", entry.ID)
	}
	entriesDir := filepath.Join(s.espPath, "loader", "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		return fmt.Errorf("create entries dir: %w", err)
	}
	content := fmt.Sprintf("title   %s\nlinux   %s\ninitrd  %s\noptions %s\n",
		entry.Title, entry.Kernel, entry.Initrd, entry.Cmdline)
	entryPath := filepath.Join(entriesDir, safeID+".conf")
	if err := os.WriteFile(entryPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write entry %s: %w", entry.ID, err)
	}
	return nil
}

// parseLoaderEntry reads a systemd-boot loader entry file.
func parseLoaderEntry(path string) (BootEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BootEntry{}, fmt.Errorf("read entry %s: %w", path, err)
	}

	entry := BootEntry{
		ID: strings.TrimSuffix(filepath.Base(path), ".conf"),
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		val := strings.Join(fields[1:], " ")
		switch strings.ToLower(key) {
		case "title":
			entry.Title = val
		case "linux":
			entry.Kernel = val
		case "initrd":
			entry.Initrd = val
		case "options":
			entry.Cmdline = val
		}
	}
	return entry, nil
}
