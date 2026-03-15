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
	"runtime"
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

	// Determine EFI binary names based on architecture.
	efiBootBin, efiFallbackBin := efiFileNames()

	// Look for the systemd-boot EFI binary in common locations.
	candidates := []string{
		filepath.Join(rootPath, "usr", "lib", "systemd", "boot", "efi", efiBootBin),
		"/bin/" + efiBootBin,
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
	dst := filepath.Join(efiDir, efiBootBin)
	if err := os.WriteFile(dst, efiData, 0o644); err != nil {
		return fmt.Errorf("write systemd-boot EFI: %w", err)
	}
	// Also copy as the default UEFI boot binary.
	fallback := filepath.Join(bootDir, efiFallbackBin)
	if err := os.WriteFile(fallback, efiData, 0o644); err != nil {
		return fmt.Errorf("write fallback %s: %w", efiFallbackBin, err)
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
		return fmt.Errorf("esp path not set, call Install first")
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
	if s.espPath == "" {
		return fmt.Errorf("esp path not set, call Install first")
	}
	// Validate entryID to prevent path traversal and invalid loader.conf entries.
	safeID := filepath.Base(entryID)
	if safeID != entryID || safeID == "." || safeID == ".." || strings.ContainsAny(entryID, "/\\") {
		return fmt.Errorf("invalid entry ID: %q", entryID)
	}
	// Strip trailing .conf if caller accidentally included it.
	entryID = strings.TrimSuffix(entryID, ".conf")
	if _, err := exec.LookPath("bootctl"); err == nil {
		cmd := exec.CommandContext(ctx, "bootctl", "set-default",
			"--esp-path="+s.espPath, entryID+".conf")
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
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) >= 1 && fields[0] == "default" {
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
	// Validate DefaultEntry to prevent invalid loader.conf content.
	defaultEntry := cfg.DefaultEntry
	if defaultEntry == "" {
		return fmt.Errorf("default entry must not be empty")
	}
	if strings.ContainsAny(defaultEntry, "/\\") || defaultEntry == ".." {
		return fmt.Errorf("invalid default entry: %q", defaultEntry)
	}
	defaultEntry = strings.TrimSuffix(defaultEntry, ".conf")
	content := fmt.Sprintf("default %s.conf\ntimeout %d\nconsole-mode max\n",
		defaultEntry, cfg.Timeout)
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
	var lines []string
	sanitize := strings.NewReplacer("\n", " ", "\r", " ")
	lines = append(lines,
		fmt.Sprintf("title   %s", sanitize.Replace(entry.Title)),
		fmt.Sprintf("linux   %s", sanitize.Replace(entry.Kernel)),
	)
	if entry.Initrd != "" {
		lines = append(lines, fmt.Sprintf("initrd  %s", sanitize.Replace(entry.Initrd)))
	}
	if entry.Cmdline != "" {
		lines = append(lines, fmt.Sprintf("options %s", sanitize.Replace(entry.Cmdline)))
	}
	content := strings.Join(lines, "\n") + "\n"
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

// efiFileNames returns the architecture-appropriate EFI binary and fallback
// boot file names.
func efiFileNames() (bootBin, fallbackBin string) {
	if runtime.GOARCH == "arm64" {
		return "systemd-bootaa64.efi", "BOOTAA64.EFI"
	}
	return "systemd-bootx64.efi", "BOOTX64.EFI"
}
