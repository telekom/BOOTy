//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
)

// legacyGRUBUnsafe holds characters that would break the double-quoted
// GRUB_CMDLINE_LINUX value of the legacy drop-in.
const legacyGRUBUnsafe = "\"\\$`\n\r"

// configureGRUBLegacy writes the kernel parameters like provision.sh of the
// CAPRF legacy provisioner: it appends "ds=nocloud <console> <extra>" to the
// image GRUB_CMDLINE_LINUX and runs update-grub when the image has it.
func (c *Configurator) configureGRUBLegacy(ctx context.Context, cfg *config.MachineConfig) error {
	extra := strings.TrimSpace(cfg.Provision.ExtraKernelParams)
	if strings.ContainsAny(extra, legacyGRUBUnsafe) {
		return fmt.Errorf("unsafe characters in ExtraKernelParams: %q", extra)
	}
	params := strings.TrimSpace("ds=nocloud " + c.legacyConsoleParam() + " " + extra)
	line := fmt.Sprintf("GRUB_CMDLINE_LINUX=\"$GRUB_CMDLINE_LINUX %s\"\n", params)

	grubDir := filepath.Join(c.rootDir, "etc", "default", "grub.d")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		return fmt.Errorf("creating grub.d dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(c.rootDir, "boot", "grub"), 0o755); err != nil {
		return fmt.Errorf("creating boot grub dir: %w", err)
	}
	grubPath := filepath.Join(grubDir, "10-caprf-kernel-params.cfg")
	slog.Info("writing GRUB config like the legacy CAPRF provisioner", "path", grubPath, "params", params)
	if err := os.WriteFile(grubPath, []byte(line), 0o644); err != nil { //nolint:gosec // fixed path inside the target root
		return fmt.Errorf("writing grub config: %w", err)
	}

	updateGRUB := ""
	for _, candidate := range []string{"/usr/sbin/update-grub", "/sbin/update-grub"} {
		if info, err := os.Stat(filepath.Join(c.rootDir, candidate)); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			updateGRUB = candidate
			break
		}
	}
	if updateGRUB == "" {
		slog.Info("update-grub not found in the target, skipping like the legacy CAPRF provisioner")
		return nil
	}
	out, err := c.disk.ChrootRun(ctx, c.rootDir, updateGRUB)
	if err != nil {
		return fmt.Errorf("update-grub: %s: %w", string(out), err)
	}
	return nil
}

// legacyConsoleParam returns console=ttyS0,115200n8, or ttyS1 on Lenovo
// hosts, like provision.sh of the CAPRF legacy provisioner.
func (c *Configurator) legacyConsoleParam() string {
	sysRoot := c.hostSysRoot
	if sysRoot == "" {
		sysRoot = "/sys"
	}
	vendor, err := os.ReadFile(filepath.Join(sysRoot, "class", "dmi", "id", "sys_vendor"))
	if err == nil && strings.EqualFold(strings.TrimSpace(string(vendor)), "lenovo") {
		return "console=ttyS1,115200n8"
	}
	return "console=ttyS0,115200n8"
}
