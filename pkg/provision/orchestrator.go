//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/image"
)

// Step represents a named provisioning step.
type Step struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Orchestrator runs the full provisioning pipeline.
type Orchestrator struct {
	cfg      *config.MachineConfig
	provider config.Provider
	disk     *disk.Manager
	config   *Configurator

	// Runtime state set during provisioning.
	targetDisk    string
	rootPartition string
	bootPartition string
}

// NewOrchestrator creates an Orchestrator with the given dependencies.
func NewOrchestrator(cfg *config.MachineConfig, provider config.Provider, diskMgr *disk.Manager) *Orchestrator {
	return &Orchestrator{
		cfg:      cfg,
		provider: provider,
		disk:     diskMgr,
		config:   NewConfigurator(diskMgr),
	}
}

// Provision runs all provisioning steps sequentially.
func (o *Orchestrator) Provision(ctx context.Context) error {
	steps := []Step{
		{"report-init", o.reportInit},
		{"set-hostname", o.setHostname},
		{"copy-provisioner-files", o.copyProvisionerFiles},
		{"configure-dns", o.configureDNS},
		{"stop-raid", o.stopRAID},
		{"remove-efi-entries", o.removeEFIBootEntries},
		{"setup-mellanox", o.setupMellanox},
		{"wipe-disks", o.wipeDisks},
		{"detect-disk", o.detectDisk},
		{"stream-image", o.streamImage},
		{"partprobe", o.partprobe},
		{"parse-partitions", o.parsePartitions},
		{"check-filesystem", o.checkFilesystem},
		{"enable-lvm", o.enableLVM},
		{"mount-root", o.mountRoot},
		{"setup-chroot-binds", o.setupChrootBinds},
		{"grow-partition", o.growPartition},
		{"resize-filesystem", o.resizeFilesystem},
		{"configure-kubelet", o.configureKubelet},
		{"configure-grub", o.configureGRUB},
		{"copy-machine-files", o.copyMachineFiles},
		{"run-machine-commands", o.runMachineCommands},
		{"teardown-chroot", o.teardownChroot},
		{"report-success", o.reportSuccess},
	}

	for _, step := range steps {
		slog.Info("Provisioning step", "step", step.Name)
		if err := step.Fn(ctx); err != nil {
			msg := fmt.Sprintf("step %s failed: %v", step.Name, err)
			slog.Error("Provisioning step failed", "step", step.Name, "error", err)
			_ = o.provider.ReportStatus(ctx, config.StatusError, msg)
			return fmt.Errorf("provision step %s: %w", step.Name, err)
		}
	}
	return nil
}

func (o *Orchestrator) reportInit(ctx context.Context) error {
	return o.provider.ReportStatus(ctx, config.StatusInit, "provisioning started")
}

func (o *Orchestrator) setHostname(_ context.Context) error {
	if o.cfg.Hostname == "" {
		return nil
	}
	return o.config.SetHostname(o.cfg)
}

func (o *Orchestrator) copyProvisionerFiles(_ context.Context) error {
	return o.config.CopyProvisionerFiles()
}

func (o *Orchestrator) configureDNS(_ context.Context) error {
	return o.config.ConfigureDNS(o.cfg)
}

func (o *Orchestrator) stopRAID(ctx context.Context) error {
	return o.disk.StopRAIDArrays(ctx)
}

func (o *Orchestrator) removeEFIBootEntries(ctx context.Context) error {
	return o.config.SetupEFIBoot(ctx)
}

func (o *Orchestrator) setupMellanox(ctx context.Context) error {
	return o.config.SetupMellanox(ctx)
}

func (o *Orchestrator) wipeDisks(ctx context.Context) error {
	return o.disk.WipeAllDisks(ctx)
}

func (o *Orchestrator) detectDisk(ctx context.Context) error {
	d, err := o.disk.DetectDisk(ctx, o.cfg.MinDiskSizeGB)
	if err != nil {
		return err
	}
	o.targetDisk = d
	return nil
}

func (o *Orchestrator) streamImage(ctx context.Context) error {
	for _, imgURL := range o.cfg.ImageURLs {
		slog.Info("Streaming image", "url", imgURL, "disk", o.targetDisk)
		if err := image.Stream(ctx, imgURL, o.targetDisk); err != nil {
			return fmt.Errorf("streaming %s: %w", imgURL, err)
		}
	}
	return nil
}

func (o *Orchestrator) partprobe(ctx context.Context) error {
	return o.disk.PartProbe(ctx, o.targetDisk)
}

func (o *Orchestrator) parsePartitions(ctx context.Context) error {
	parts, err := o.disk.ParsePartitions(ctx, o.targetDisk)
	if err != nil {
		return err
	}

	boot, err := o.disk.FindBootPartition(parts)
	if err != nil {
		slog.Warn("No EFI partition found", "error", err)
	} else {
		o.bootPartition = boot.Node
	}

	root, err := o.disk.FindRootPartition(parts)
	if err != nil {
		return err
	}
	o.rootPartition = root.Node
	return nil
}

func (o *Orchestrator) checkFilesystem(ctx context.Context) error {
	return o.disk.CheckFilesystem(ctx, o.rootPartition)
}

func (o *Orchestrator) enableLVM(ctx context.Context) error {
	return o.disk.EnableLVM(ctx)
}

func (o *Orchestrator) mountRoot(ctx context.Context) error {
	return o.disk.MountPartition(ctx, o.rootPartition, newroot)
}

func (o *Orchestrator) setupChrootBinds(_ context.Context) error {
	return o.disk.SetupChrootBindMounts(newroot)
}

func (o *Orchestrator) growPartition(ctx context.Context) error {
	partNum := disk.PartitionNumber(o.rootPartition, o.targetDisk)
	if partNum == 0 {
		slog.Warn("Could not determine partition number, skipping grow")
		return nil
	}
	return o.disk.GrowPartition(ctx, o.targetDisk, partNum)
}

func (o *Orchestrator) resizeFilesystem(ctx context.Context) error {
	return o.disk.ResizeFilesystem(ctx, o.rootPartition)
}

func (o *Orchestrator) configureKubelet(_ context.Context) error {
	return o.config.ConfigureKubelet(o.cfg)
}

func (o *Orchestrator) configureGRUB(ctx context.Context) error {
	return o.config.ConfigureGRUB(ctx, o.cfg)
}

func (o *Orchestrator) copyMachineFiles(_ context.Context) error {
	return o.config.CopyMachineFiles()
}

func (o *Orchestrator) runMachineCommands(ctx context.Context) error {
	return o.config.RunMachineCommands(ctx)
}

func (o *Orchestrator) teardownChroot(_ context.Context) error {
	o.disk.TeardownChrootBindMounts(newroot)
	return o.disk.Unmount(newroot)
}

func (o *Orchestrator) reportSuccess(ctx context.Context) error {
	return o.provider.ReportStatus(ctx, config.StatusSuccess, "provisioning complete")
}
