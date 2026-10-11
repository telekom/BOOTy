//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
)

// legacyGRUBUnsafe holds characters that would break the double-quoted
// GRUB_CMDLINE_LINUX value of the legacy drop-in.
const legacyGRUBUnsafe = "\"\\$`\n\r"

// legacyNoUpdateGRUB marks a target root without update-grub.
const legacyNoUpdateGRUB = "BOOTY_NO_UPDATE_GRUB"

// legacyUpdateGRUBScript runs update-grub from /usr/sbin or /sbin like the
// legacy provision.sh, or reports that the image has none.
const legacyUpdateGRUBScript = "if [ -x /usr/sbin/update-grub ]; then exec /usr/sbin/update-grub; " +
	"elif [ -x /sbin/update-grub ]; then exec /sbin/update-grub; " +
	"else echo " + legacyNoUpdateGRUB + "; fi"

// configureGRUBLegacy writes the kernel parameters like provision.sh of the
// CAPRF legacy provisioner: it appends "ds=nocloud <console> <extra>" to the
// image GRUB_CMDLINE_LINUX and runs update-grub when the image has it.
func (c *Configurator) configureGRUBLegacy(ctx context.Context, cfg *config.MachineConfig) error {
	extra := strings.TrimSpace(cfg.Provision.ExtraKernelParams)
	if strings.ContainsAny(extra, legacyGRUBUnsafe) {
		return fmt.Errorf("unsafe characters in ExtraKernelParams: %q", extra)
	}
	abRoot, err := abRootKernelParam(cfg)
	if err != nil {
		return err
	}
	params := strings.Join(strings.Fields("ds=nocloud "+c.legacyConsoleParam()+" "+extra+" "+abRoot), " ")
	line := fmt.Sprintf("GRUB_CMDLINE_LINUX=\"$GRUB_CMDLINE_LINUX %s\"\n", params)

	// targetPath refuses paths that leave the target root through symlinks.
	if _, err := c.targetPath(path.Join("/boot/grub", "grub.cfg")); err != nil {
		return fmt.Errorf("creating boot grub dir: %w", err)
	}
	grubPath, err := c.targetPath("/etc/default/grub.d/10-caprf-kernel-params.cfg")
	if err != nil {
		return fmt.Errorf("grub drop-in path: %w", err)
	}
	slog.Info("writing GRUB config like the legacy CAPRF provisioner", "path", grubPath)
	if err := writeFileAtomic(grubPath, []byte(line), 0o644); err != nil {
		return fmt.Errorf("writing grub config: %w", err)
	}

	// Look for update-grub inside the target root, so symlinks in the image
	// resolve against the image and never against the ramdisk.
	out, err := c.disk.ChrootRun(ctx, c.rootDir, legacyUpdateGRUBScript)
	if err != nil {
		return fmt.Errorf("update-grub: %s: %w", string(out), err)
	}
	if strings.Contains(string(out), legacyNoUpdateGRUB) {
		slog.Info("update-grub not found in the target, skipping like the legacy CAPRF provisioner")
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
