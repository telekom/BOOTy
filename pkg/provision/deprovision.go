//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/telekom/BOOTy/pkg/config"
)

// Deprovision runs the deprovisioning pipeline.
// Mode "soft" renames grub.cfg to make the system unbootable.
// Mode "hard" (default) wipes all disks and removes EFI boot entries.
func (o *Orchestrator) Deprovision(ctx context.Context) error {
	mode := o.cfg.Mode
	if mode == "" {
		mode = "hard"
	}
	slog.Info("Starting deprovisioning", "mode", mode)

	steps := []Step{
		{"report-init", o.reportInit},
		{"copy-provisioner-files", o.copyProvisionerFiles},
		{"configure-dns", o.configureDNS},
	}

	if mode == "soft" {
		steps = append(steps, Step{"soft-deprovision", o.softDeprovision})
	} else {
		steps = append(steps,
			Step{"stop-raid", o.stopRAID},
			Step{"disable-lvm", o.disableLVM},
			Step{"wipe-disks", o.wipeOrSecureEraseDisks},
			Step{"remove-efi-entries", o.removeEFIBootEntries},
		)
	}
	steps = append(steps, Step{"report-success", o.reportDeprovisionSuccess})

	for i, step := range steps {
		slog.Info("Deprovisioning step", "step", step.Name, "index", i+1, "total", len(steps))
		if err := step.Fn(ctx); err != nil {
			msg := fmt.Sprintf("step %s failed: %v", step.Name, err)
			slog.Error("Deprovisioning step failed", "step", step.Name, "error", err)
			DumpDebugState(step.Name)
			if reportErr := o.provider.ReportStatus(ctx, config.StatusError, msg); reportErr != nil {
				slog.Error("Failed to report error status", "error", reportErr)
			}
			return fmt.Errorf("deprovision step %s: %w", step.Name, err)
		}
	}
	return nil
}

// softDeprovision renames grub.cfg so the system won't boot.
func (o *Orchestrator) softDeprovision(ctx context.Context) error {
	// Detect disk and mount root.
	d, err := o.disk.DetectDisk(ctx, o.cfg.MinDiskSizeGB)
	if err != nil {
		return fmt.Errorf("detecting disk: %w", err)
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
		slog.Info("Renaming grub.cfg", "from", grubCfg, "to", grubBak)
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
