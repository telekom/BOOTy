//go:build linux

package bootloader

import (
	"bufio"
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

// GRUB implements the Bootloader interface for GRUB2.
type GRUB struct {
	Log      *slog.Logger
	rootPath string
	espPath  string
}

// NewGRUB creates a new GRUB bootloader manager.
func NewGRUB(log *slog.Logger) *GRUB {
	return &GRUB{Log: log}
}

// Name returns the bootloader type.
func (g *GRUB) Name() string { return "grub" }

// Install installs GRUB into the provisioned OS via chroot.
func (g *GRUB) Install(ctx context.Context, rootPath, espPath string) error {
	g.rootPath = rootPath
	g.espPath = espPath

	// Detect grub vs grub2 binary name
	grubBin := "grub-install"
	if _, err := os.Stat(filepath.Join(rootPath, "usr", "sbin", "grub2-install")); err == nil {
		grubBin = "grub2-install"
	}

	// Determine grub target based on architecture.
	var grubTarget string
	if runtime.GOARCH == "arm64" {
		grubTarget = "arm64-efi"
	} else {
		grubTarget = "x86_64-efi"
	}

	// Translate espPath to chroot-relative path.
	chrootESP := espPath
	if strings.HasPrefix(espPath, rootPath) {
		chrootESP = strings.TrimPrefix(espPath, rootPath)
		if chrootESP == "" {
			chrootESP = "/"
		}
	}

	cmd := exec.CommandContext(ctx, "chroot", rootPath,
		grubBin,
		"--target="+grubTarget,
		"--efi-directory="+chrootESP,
		"--no-nvram",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s: %w", grubBin, string(out), err)
	}
	g.Log.Info("grub installed", "binary", grubBin)
	return nil
}

// Configure writes GRUB defaults and runs update-grub.
func (g *GRUB) Configure(ctx context.Context, cfg *BootConfig) error {
	if g.rootPath == "" {
		return fmt.Errorf("root path not set, call Install first")
	}
	grubDefault := filepath.Join(g.rootPath, "etc", "default", "grub")

	lines := []string{
		fmt.Sprintf("GRUB_DEFAULT=%q", cfg.DefaultEntry),
		fmt.Sprintf("GRUB_TIMEOUT=%d", cfg.Timeout),
		fmt.Sprintf("GRUB_CMDLINE_LINUX_DEFAULT=%q", cfg.KernelCmdline),
	}
	if cfg.ExtraParams != "" {
		lines = append(lines, fmt.Sprintf("GRUB_CMDLINE_LINUX=%q", cfg.ExtraParams))
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(grubDefault), 0o755); err != nil {
		return fmt.Errorf("create grub default dir: %w", err)
	}
	if err := os.WriteFile(grubDefault, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write grub defaults: %w", err)
	}

	// Run update-grub / grub2-mkconfig in chroot
	mkconfig := "update-grub"
	if _, err := os.Stat(filepath.Join(g.rootPath, "usr", "sbin", "grub2-mkconfig")); err == nil {
		mkconfig = "grub2-mkconfig"
	}

	cmd := exec.CommandContext(ctx, "chroot", g.rootPath, mkconfig, "-o", "/boot/grub/grub.cfg")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s: %w", mkconfig, string(out), err)
	}
	g.Log.Info("grub configured", "cmdline", cfg.KernelCmdline)
	return nil
}

// ListEntries returns available boot entries from grub.cfg.
func (g *GRUB) ListEntries(_ context.Context, rootPath string) ([]BootEntry, error) {
	grubCfg := filepath.Join(rootPath, "boot", "grub", "grub.cfg")
	if _, err := os.Stat(grubCfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat grub config: %w", err)
		}
		// Try grub2 path
		grubCfg = filepath.Join(rootPath, "boot", "grub2", "grub.cfg")
	}
	return parseGRUBConfig(grubCfg)
}

// SetDefault sets the default boot entry via grub-set-default.
func (g *GRUB) SetDefault(ctx context.Context, entryID string) error {
	if g.rootPath == "" {
		return fmt.Errorf("root path not set, call Install first")
	}
	cmd := exec.CommandContext(ctx, "chroot", g.rootPath, "grub-set-default", entryID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("grub-set-default %s: %s: %w", entryID, string(out), err)
	}
	return nil
}

// parseGRUBConfig extracts boot entries from a grub.cfg file.
func parseGRUBConfig(path string) ([]BootEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open grub config: %w", err)
	}
	defer func() { _ = f.Close() }()

	var entries []BootEntry
	var current *BootEntry
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "menuentry ") {
			title := extractQuoted(line)
			current = &BootEntry{
				ID:    fmt.Sprintf("%d", len(entries)),
				Title: title,
			}
			continue
		}

		if current == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "linux"):
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				current.Kernel = parts[1]
			}
			if len(parts) >= 3 {
				current.Cmdline = strings.Join(parts[2:], " ")
			}
		case strings.HasPrefix(line, "initrd"):
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				current.Initrd = parts[1]
			}
		case line == "}":
			if current.Kernel != "" {
				entries = append(entries, *current)
			}
			current = nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan grub config: %w", err)
	}
	return entries, nil
}

// extractQuoted extracts the first quoted string (single or double) from a line.
func extractQuoted(line string) string {
	for _, q := range []byte{'\'', '"'} {
		start := strings.IndexByte(line, q)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(line[start+1:], q)
		if end < 0 {
			continue
		}
		return line[start+1 : start+1+end]
	}
	return ""
}
