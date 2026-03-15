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

// Install installs systemd-boot via bootctl, falling back to manual EFI
// binary copy when bootctl is not available in the initramfs.
func (s *SystemdBoot) Install(ctx context.Context, rootPath, espPath string) error {
	s.rootPath = rootPath
	s.espPath = espPath

	if _, err := exec.LookPath("bootctl"); err == nil {
		cmd := exec.CommandContext(ctx, "bootctl", "install",
			"--esp-path="+espPath,
			"--root="+rootPath,
		)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			s.Log.Warn("bootctl install failed, trying manual install", "error", runErr, "output", string(out))
		} else {
			s.Log.Info("systemd-boot installed via bootctl", "esp", espPath)
			return nil
		}
	}

	// Manual fallback: copy the systemd-boot EFI binary from the target OS
	// or from a bundled copy in the initramfs.
	efiDir := filepath.Join(espPath, "EFI", "systemd")
	bootDir := filepath.Join(espPath, "EFI", "BOOT")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		return fmt.Errorf("create EFI/systemd directory: %w", err)
	}
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		return fmt.Errorf("create EFI/BOOT directory: %w", err)
	}

	// Look for the systemd-boot EFI binary in common locations.
	candidates := []string{
		filepath.Join(rootPath, "usr", "lib", "systemd", "boot", "efi", "systemd-bootx64.efi"),
		"/bin/systemd-bootx64.efi",
	}
	var srcEFI string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			srcEFI = c
			break
		}
	}
	if srcEFI == "" {
		return errors.New("systemd-boot EFI binary not found in target OS or initramfs")
	}

	efiData, err := os.ReadFile(srcEFI)
	if err != nil {
		return fmt.Errorf("read systemd-boot EFI: %w", err)
	}
	dst := filepath.Join(efiDir, "systemd-bootx64.efi")
	if err := os.WriteFile(dst, efiData, 0o644); err != nil {
		return fmt.Errorf("write systemd-boot EFI: %w", err)
	}
	// Also copy as the default UEFI boot binary.
	fallback := filepath.Join(bootDir, "BOOTX64.EFI")
	if err := os.WriteFile(fallback, efiData, 0o644); err != nil {
		return fmt.Errorf("write fallback BOOTX64.EFI: %w", err)
	}

	// Create loader directory for configuration files.
	loaderDir := filepath.Join(espPath, "loader")
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		return fmt.Errorf("create loader directory: %w", err)
	}

	s.Log.Info("systemd-boot installed manually", "esp", espPath, "source", srcEFI)
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

// SetDefault sets the default boot entry via bootctl, falling back to writing
// loader.conf directly when bootctl is not available.
func (s *SystemdBoot) SetDefault(ctx context.Context, entryID string) error {
	if _, err := exec.LookPath("bootctl"); err == nil {
		cmd := exec.CommandContext(ctx, "bootctl", "set-default", entryID+".conf")
		out, runErr := cmd.CombinedOutput()
		if runErr == nil {
			return nil
		}
		s.Log.Warn("bootctl set-default failed, falling back to loader.conf", "error", runErr, "output", string(out))
	}

	// Fallback: write default entry directly into loader.conf.
	loaderConf := filepath.Join(s.espPath, "loader", "loader.conf")
	data, err := os.ReadFile(loaderConf)
	if err != nil {
		return fmt.Errorf("read loader.conf for set-default: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "default") {
			lines[i] = "default " + entryID + ".conf"
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "default "+entryID+".conf")
	}
	if err := os.WriteFile(loaderConf, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return fmt.Errorf("write loader.conf: %w", err)
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
