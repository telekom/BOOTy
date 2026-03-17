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

	"github.com/telekom/BOOTy/pkg/grubcfg"
)

// GRUB implements the Bootloader interface for GRUB2.
type GRUB struct {
	Log      *slog.Logger
	rootPath string
}

// NewGRUB creates a new GRUB bootloader manager.
func NewGRUB(log *slog.Logger) *GRUB {
	return &GRUB{Log: log}
}

// Name returns the bootloader type.
func (g *GRUB) Name() string { return "grub" }

// Install installs GRUB into the provisioned OS via chroot.
// NOTE: espPath must be a subdirectory of rootPath (e.g., rootPath/boot/efi),
// because GRUB is installed via chroot and the ESP path is computed relative
// to rootPath. This differs from SystemdBoot.Install, which passes espPath
// directly to bootctl as a host filesystem path.
func (g *GRUB) Install(ctx context.Context, rootPath, espPath string) error {
	g.rootPath = rootPath

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
	rootClean := filepath.Clean(rootPath)
	espClean := filepath.Clean(espPath)
	rel, relErr := filepath.Rel(rootClean, espClean)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("esp path %q is not inside root %q", espPath, rootPath)
	}
	chrootESP := "/" + rel

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
	if cfg == nil {
		return fmt.Errorf("boot config must not be nil")
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
	cfgPath := "/boot/grub/grub.cfg"
	if _, err := os.Stat(filepath.Join(g.rootPath, "usr", "sbin", "grub2-mkconfig")); err == nil {
		mkconfig = "grub2-mkconfig"
		cfgPath = "/boot/grub2/grub.cfg"
	}

	cmd := exec.CommandContext(ctx, "chroot", g.rootPath, mkconfig, "-o", cfgPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s: %w", mkconfig, string(out), err)
	}
	g.Log.Info("grub configured", "defaultEntry", cfg.DefaultEntry)
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
	setBin := "grub-set-default"
	if _, err := os.Stat(filepath.Join(g.rootPath, "usr", "sbin", "grub2-set-default")); err == nil {
		setBin = "grub2-set-default"
	}
	cmd := exec.CommandContext(ctx, "chroot", g.rootPath, setBin, entryID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", setBin, entryID, string(out), err)
	}
	return nil
}

// parseGRUBConfig extracts boot entries from a grub.cfg file.
func parseGRUBConfig(path string) ([]BootEntry, error) {
	parsedEntries, err := grubcfg.ParseFile(path)
	if err != nil {
		return nil, err
	}

	entries := make([]BootEntry, 0, len(parsedEntries))
	for _, entry := range parsedEntries {
		if entry.Kernel == "" {
			continue
		}
		entries = append(entries, BootEntry{
			ID:      fmt.Sprintf("%d", len(entries)),
			Title:   entry.Title,
			Kernel:  entry.Kernel,
			Initrd:  entry.Initrd,
			Cmdline: entry.Cmdline,
		})
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
