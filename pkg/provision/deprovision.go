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

// Deprovision runs the deprovisioning pipeline.
// Mode "soft-deprovision" (or "soft") renames grub.cfg to make the system unbootable.
// Mode "deprovision" or "hard" (default) wipes all disks and removes EFI boot entries.
func (o *Orchestrator) Deprovision(ctx context.Context) error {
	mode := o.cfg.Mode
	if mode == "" {
		mode = "hard"
	}
	o.cfg.Mode = mode
	o.log.Info("starting deprovisioning", "mode", mode)

	steps := []Step{
		{"report-init", o.reportInit},
		{"copy-provisioner-files", o.copyProvisionerFiles},
		{"configure-dns", o.configureDNS},
	}

	if mode == "soft" || mode == "soft-deprovision" {
		steps = append(steps, Step{"soft-deprovision", o.softDeprovision})
	} else {
		steps = append(steps,
			Step{"select-deprovision-disk", o.selectDeprovisionDisk},
			Step{"stop-raid", o.stopRAID},
			Step{"disable-lvm", o.disableLVM},
			Step{"wipe-disks", o.wipeOrSecureEraseDisks},
			Step{"mount-efivarfs", o.mountEFIVars},
			Step{"remove-efi-entries", o.removeEFIBootEntries},
		)
	}
	steps = append(steps, Step{"report-success", o.reportDeprovisionSuccess})

	for i, step := range steps {
		o.log.Info("Deprovisioning step", "step", step.Name, "index", i+1, "total", len(steps))
		if err := step.Fn(ctx); err != nil {
			msg := fmt.Sprintf("step %s failed: %v", step.Name, err)
			o.log.Error("Deprovisioning step failed", "step", step.Name, "error", err)
			DumpDebugState(step.Name)
			if reportErr := o.provider.ReportStatus(ctx, config.StatusError, msg); reportErr != nil {
				o.log.Error("Failed to report error status", "error", reportErr)
			}
			return fmt.Errorf("deprovision step %s: %w", step.Name, err)
		}
	}
	return nil
}

func (o *Orchestrator) selectDeprovisionDisk(_ context.Context) error {
	device, err := o.configuredDeprovisionDisk()
	if err != nil {
		return err
	}
	if device == "" {
		o.log.Info("no deprovision disk override configured; hard deprovision will wipe all disks")
		return nil
	}
	o.targetDisk = device
	o.log.Info("using configured deprovision disk", "device", device)
	return nil
}

func (o *Orchestrator) configuredDeprovisionDisk() (string, error) {
	for _, candidate := range []struct {
		name   string
		device string
	}{
		{name: "deprovision.device", device: o.cfg.Deprovision.Device},
		{name: "provision.disk.device", device: o.cfg.Provision.Disk.Device},
	} {
		device := strings.TrimSpace(candidate.device)
		if device == "" {
			continue
		}
		if err := validateConfiguredBlockDevice(candidate.name, device); err != nil {
			return "", err
		}
		return device, nil
	}
	return "", nil
}

func validateConfiguredBlockDevice(name, device string) error {
	if !strings.HasPrefix(device, "/dev/") {
		return fmt.Errorf("%s %q must be an absolute /dev/ path", name, device)
	}
	info, err := statPath(device)
	if err != nil {
		return fmt.Errorf("%s %q: %w", name, device, err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("%s %q is not a block device", name, device)
	}
	return nil
}

// softDeprovision renames grub.cfg so the system won't boot.
func (o *Orchestrator) softDeprovision(ctx context.Context) error {
	var d string
	var err error
	if dev, devErr := o.configuredDeprovisionDisk(); devErr != nil {
		return devErr
	} else if dev != "" {
		d = dev
	} else {
		d, err = o.disk.DetectDisk(ctx, o.cfg.Provision.Disk.MinSizeGB)
		if err != nil {
			return fmt.Errorf("detecting disk: %w", err)
		}
	}
	if err := o.disk.PartProbe(ctx, d); err != nil {
		return fmt.Errorf("partprobe: %w", err)
	}
	parts, err := o.disk.ParsePartitions(ctx, d)
	if err != nil {
		return fmt.Errorf("parsing partitions: %w", err)
	}
	root, err := o.disk.FindRootPartition(parts)
	if err != nil {
		return fmt.Errorf("finding root: %w", err)
	}
	if err := o.disk.MountPartition(ctx, root.Node, newroot); err != nil {
		return fmt.Errorf("mounting root: %w", err)
	}
	defer func() { _ = o.disk.Unmount(newroot) }()

	// Rename grub.cfg → grub.cfg.bak.
	grubCfg := filepath.Join(newroot, "boot", "grub", "grub.cfg")
	grubBak := grubCfg + ".bak"
	if _, err := os.Stat(grubCfg); err == nil {
		slog.Info("renaming grub.cfg", "from", grubCfg, "to", grubBak)
		if err := os.Rename(grubCfg, grubBak); err != nil {
			return fmt.Errorf("renaming grub.cfg: %w", err)
		}
	} else {
		slog.Warn("grub.cfg not found", "path", grubCfg)
	}
	return nil
}

func (o *Orchestrator) reportDeprovisionSuccess(ctx context.Context) error {
	return o.provider.ReportStatus(ctx, config.StatusSuccess, "deprovisioning complete")
}
