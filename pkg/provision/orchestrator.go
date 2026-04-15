//go:build linux

package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/telekom/BOOTy/pkg/cloudinit"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/executil"
	"github.com/telekom/BOOTy/pkg/firmware"
	"github.com/telekom/BOOTy/pkg/health"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/inventory"
	"github.com/telekom/BOOTy/pkg/rescue"
)

// Step represents a named provisioning step.
type Step struct {
	Name string
	Fn   func(ctx context.Context) error
}

// HealthReporter is an optional provider capability for reporting health check results.
type HealthReporter interface {
	ReportHealthChecks(context.Context, []health.CheckResult) error
}

// Orchestrator runs the full provisioning pipeline.
type Orchestrator struct {
	cfg      *config.MachineConfig
	provider config.Provider
	disk     *disk.Manager
	config   *Configurator
	log      *slog.Logger

	// Runtime state set during provisioning.
	targetDisk      string
	rootPartition   string
	bootPartition   string
	bestImageURL    string // resolved by verify-image, reused by stream-image
	firmwareChanged bool   // true if any step changed firmware values requiring hard reboot
}

// NewOrchestrator creates an Orchestrator with the given dependencies.
func NewOrchestrator(cfg *config.MachineConfig, provider config.Provider, diskMgr *disk.Manager) *Orchestrator {
	return &Orchestrator{
		cfg:      cfg,
		provider: provider,
		disk:     diskMgr,
		config:   NewConfigurator(diskMgr),
		log:      slog.Default().With("component", "provision"),
	}
}

// provisionSteps returns the ordered list of provisioning steps.
func (o *Orchestrator) provisionSteps() []Step {
	return []Step{
		{"report-init", o.reportInit},
		{"collect-inventory", o.collectInventory},
		{"collect-firmware", o.collectFirmware},
		{"health-checks", o.runHealthChecks},
		{"set-hostname", o.setHostname},
		{"copy-provisioner-files", o.copyProvisionerFiles},
		{"configure-dns", o.configureDNS},
		{"stop-raid", o.stopRAID},
		{"disable-lvm", o.disableLVM},
		{"mount-efivarfs", o.mountEFIVars},
		{"remove-efi-entries", o.removeEFIBootEntries},
		{"setup-mellanox", o.setupMellanox},
		{"setup-nvme-namespaces", o.setupNVMeNamespaces},
		{"detect-disk", o.detectDisk},
		{"wipe-disks", o.wipeOrSecureEraseDisks},
		{"verify-image", o.verifyImageSignature},
		{"apply-partition-layout", o.applyPartitionLayout},
		{"stream-image", o.streamImage},
		{"partprobe", o.partprobe},
		{"parse-partitions", o.parsePartitions},
		{"check-filesystem", o.checkFilesystem},
		{"enable-lvm", o.enableLVM},
		{"mount-root", o.mountRoot},
		{"write-fstab", o.writeFstabStep},
		{"setup-chroot-binds", o.setupChrootBinds},
		{"grow-partition", o.growPartition},
		{"resize-filesystem", o.resizeFilesystem},
		{"configure-kubelet", o.configureKubelet},
		{"configure-grub", o.configureGRUB},
		{"inject-cloudinit", o.injectCloudInit},
		{"copy-machine-files", o.copyMachineFiles},
		{"run-machine-commands", o.runMachineCommands},
		{"run-post-provision-cmds", o.runPostProvisionCmds},
		{"create-efi-boot-entry", o.createEFIBootEntry},
		{"teardown-chroot", o.teardownChroot},
		{"report-success", o.reportSuccess},
	}
}

// Provision runs all provisioning steps sequentially.
func (o *Orchestrator) Provision(ctx context.Context) error {
	steps := o.provisionSteps()

	cp := o.loadOrCreateCheckpoint()

	// stateSteps must always re-run on resume because they rebuild in-memory
	// runtime fields that later steps depend on (firmwareChanged, targetDisk,
	// rootPartition/bootPartition).
	stateSteps := resumeStateSteps()

	for i, step := range steps {
		_, mustRun := stateSteps[step.Name]
		if cp.IsCompleted(step.Name) && !mustRun {
			o.log.Info("skipping completed step", "step", step.Name)
			continue
		}
		o.log.Info("provisioning step", "step", step.Name, "index", i+1, "total", len(steps))
		if err := o.executeStep(ctx, step, cp); err != nil {
			return err
		}
	}

	if rmErr := cp.Remove(); rmErr != nil {
		o.log.Warn("failed to remove checkpoint", "error", rmErr)
	}
	return nil
}

func resumeStateSteps() map[string]struct{} {
	return map[string]struct{}{
		"setup-mellanox":   {},
		"detect-disk":      {},
		"parse-partitions": {},
	}
}

// loadOrCreateCheckpoint loads an existing checkpoint when BOOTY_RESUME is set,
// or returns a fresh checkpoint. Only checkpoints created via BOOTY_RESUME
// persist to disk; otherwise Save/Remove are no-ops.
func (o *Orchestrator) loadOrCreateCheckpoint() *Checkpoint {
	if enabled, _ := strconv.ParseBool(os.Getenv("BOOTY_RESUME")); enabled {
		cp, cpErr := LoadCheckpoint()
		if cpErr != nil && !errors.Is(cpErr, ErrNoCheckpoint) {
			o.log.Warn("failed to load checkpoint, starting fresh", "error", cpErr)
		}
		if cp != nil {
			return cp
		}
		return &Checkpoint{persist: true}
	}
	return &Checkpoint{}
}

// executeStep runs a single provisioning step with optional retry, updating
// the checkpoint on success or failure.
func (o *Orchestrator) executeStep(ctx context.Context, step Step, cp *Checkpoint) error {
	var err error
	if policy, ok := DefaultPolicies[step.Name]; ok {
		err = WithRetry(ctx, step.Name, policy, step.Fn)
	} else {
		err = step.Fn(ctx)
	}

	if err != nil {
		msg := fmt.Sprintf("step %s failed: %v", step.Name, err)
		o.log.Error("provisioning step failed", "step", step.Name, "error", err)
		cp.Errors = append(cp.Errors, msg)
		cp.FailureCount++
		if saveErr := cp.Save(); saveErr != nil {
			o.log.Warn("failed to save checkpoint", "error", saveErr)
		}
		DumpDebugState(step.Name)
		dumpConfig(o.cfg)
		if reportErr := o.provider.ReportStatus(ctx, config.StatusError, msg); reportErr != nil {
			o.log.Error("failed to report error status", "error", reportErr)
		}
		return fmt.Errorf("provision step %s: %w", step.Name, err)
	}

	cp.MarkStep(step.Name)
	if saveErr := cp.Save(); saveErr != nil {
		o.log.Warn("failed to save checkpoint", "error", saveErr)
	}
	return nil
}

// RescueConfig returns the normalized rescue config derived from machine config.
func (o *Orchestrator) RescueConfig() *rescue.Config {
	cfg := &rescue.Config{Mode: rescue.ModeReboot}
	if o.cfg.RescueMode != "" {
		mode, err := rescue.ParseMode(o.cfg.RescueMode)
		if err != nil {
			o.log.Warn("invalid rescue mode, defaulting to reboot", "mode", o.cfg.RescueMode, "error", err)
		} else {
			cfg.Mode = mode
		}
	}
	if o.cfg.RescueSSHPubKey != "" {
		cfg.SSHKeys = []string{o.cfg.RescueSSHPubKey}
	}
	cfg.PasswordHash = o.cfg.RescuePasswordHash
	if o.cfg.RescueTimeout > 0 {
		cfg.ShellTimeout = time.Duration(o.cfg.RescueTimeout) * time.Second
	}
	cfg.AutoMountDisks = o.cfg.RescueAutoMountDisks
	cfg.ApplyDefaults()
	return cfg
}

// RescueAction returns the rescue action to take after a provisioning failure,
// based on the machine config's RescueMode setting.
func (o *Orchestrator) RescueAction(state *rescue.RetryState) rescue.Action {
	cfg := o.RescueConfig()
	return rescue.Decide(cfg, state)
}

func (o *Orchestrator) reportInit(ctx context.Context) error {
	return o.provider.ReportStatus(ctx, config.StatusInit, "provisioning started")
}

func (o *Orchestrator) collectInventory(ctx context.Context) error {
	if !o.cfg.InventoryEnabled {
		o.log.Info("Hardware inventory collection disabled, skipping")
		return nil
	}

	inv, err := inventory.Collect()
	if err != nil {
		o.log.Warn("Hardware inventory collection failed", "error", err)
		return nil // non-fatal
	}

	data, err := json.Marshal(inv)
	if err != nil {
		o.log.Warn("Failed to marshal hardware inventory", "error", err)
		return nil // non-fatal
	}

	o.log.Info("Hardware inventory collected",
		"cpus", len(inv.CPUs),
		"disks", len(inv.Disks),
		"nics", len(inv.NICs),
		"pci_devices", len(inv.PCIDevices))

	if err := o.provider.ReportInventory(ctx, data); err != nil {
		o.log.Warn("Failed to report hardware inventory", "error", err)
	}
	return nil
}

func (o *Orchestrator) collectFirmware(ctx context.Context) error {
	if !o.cfg.FirmwareEnabled {
		o.log.Info("Firmware reporting disabled, skipping")
		return nil
	}

	report, err := firmware.Collect()
	if err != nil {
		// Collection is best-effort: missing sysfs entries are common in
		// virtual environments, so we log and continue provisioning.
		o.log.Warn("Firmware collection failed, continuing", "error", err)
		return nil
	}

	if o.cfg.FirmwareMinBIOS != "" || o.cfg.FirmwareMinBMC != "" {
		policy := firmware.Policy{
			MinBIOSVersion: o.cfg.FirmwareMinBIOS,
			MinBMCVersion:  o.cfg.FirmwareMinBMC,
		}
		results := firmware.Validate(report, policy)
		for _, r := range results {
			if r.Status == "fail" {
				o.log.Warn("Firmware validation", "name", r.Name, "status", r.Status, "message", r.Message)
			} else {
				o.log.Info("Firmware validation", "name", r.Name, "status", r.Status, "message", r.Message)
			}
		}
	}

	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal firmware report: %w", err)
	}

	return o.provider.ReportFirmware(ctx, data)
}

func (o *Orchestrator) setHostname(_ context.Context) error {
	if o.cfg.Hostname == "" {
		return nil
	}
	return o.config.SetHostname(o.cfg)
}

func (o *Orchestrator) copyProvisionerFiles(ctx context.Context) error {
	return o.config.CopyProvisionerFiles(ctx)
}

func (o *Orchestrator) configureDNS(_ context.Context) error {
	return o.config.ConfigureDNS(o.cfg)
}

func (o *Orchestrator) stopRAID(ctx context.Context) error {
	return o.disk.StopRAIDArrays(ctx)
}

func (o *Orchestrator) disableLVM(ctx context.Context) error {
	return o.disk.DisableLVM(ctx)
}

func (o *Orchestrator) removeEFIBootEntries(ctx context.Context) error {
	return o.config.RemoveEFIBootEntries(ctx)
}

func (o *Orchestrator) mountEFIVars(ctx context.Context) error {
	return o.config.MountEFIVars(ctx)
}

func (o *Orchestrator) createEFIBootEntry(ctx context.Context) error {
	return o.config.CreateEFIBootEntry(ctx, o.targetDisk, o.bootPartition)
}

func (o *Orchestrator) setupMellanox(ctx context.Context) error {
	changed, err := o.config.SetupMellanox(ctx, o.cfg.NumVFs)
	if err != nil {
		return err
	}
	if changed {
		o.firmwareChanged = true
	}
	return nil
}

// FirmwareChanged reports whether any provisioning step changed firmware values
// that require a hard reboot (not kexec) to reinitialize.
func (o *Orchestrator) FirmwareChanged() bool {
	return o.firmwareChanged
}

func (o *Orchestrator) setupNVMeNamespaces(ctx context.Context) error {
	if o.cfg.NVMeNamespaces == "" {
		return nil
	}
	cfgs, err := disk.ParseNVMeConfig(o.cfg.NVMeNamespaces)
	if err != nil {
		return fmt.Errorf("parsing nvme namespace layout: %w", err)
	}
	created, err := o.disk.ApplyNVMeNamespaceLayout(ctx, cfgs)
	if err != nil {
		return err
	}
	// Verify at least one namespace was created across all controllers.
	totalCreated := 0
	for _, nsids := range created {
		totalCreated += len(nsids)
	}
	if totalCreated == 0 {
		return fmt.Errorf("nvme namespace layout applied but no namespaces were created; check controller support and configuration")
	}

	// After namespace creation set DiskDevice to the first created namespace on
	// the first configured controller so DetectDisk targets the intended OS disk.
	if len(cfgs) > 0 && o.cfg.DiskDevice == "" {
		firstController := cfgs[0].Controller
		nsids := created[firstController]
		if len(nsids) > 0 {
			o.cfg.DiskDevice = firstController + "n" + nsids[0]
			o.log.Info("set disk device from nvme namespace layout", "device", o.cfg.DiskDevice)
		}
	}
	return nil
}

func (o *Orchestrator) wipeOrSecureEraseDisks(ctx context.Context) error {
	if err := o.validatePartitionLayoutConfig(); err != nil {
		return err
	}

	if o.cfg.SecureErase {
		o.log.Info("Secure erase enabled, performing hardware-level erase")
		return o.disk.SecureEraseAllDisks(ctx)
	}
	return o.disk.WipeAllDisks(ctx)
}

// errPartitionLayoutNotSupported is the shared error for layout-mode gating.
const errPartitionLayoutNotSupported = "partition layout provisioning is not supported yet; rootfs extraction support is still pending"

func (o *Orchestrator) validatePartitionLayoutModeCompatibility() error {
	if o.cfg.PartitionLayout == nil {
		return nil
	}

	// Deprovisioning is allowed to wipe disks even when PARTITION_LAYOUT is set.
	if o.cfg.Mode == "deprovision" || o.cfg.Mode == "soft" || o.cfg.Mode == "soft-deprovision" {
		return nil
	}

	return fmt.Errorf("%s", errPartitionLayoutNotSupported)
}

func (o *Orchestrator) validatePartitionLayoutConfig() error {
	if o.cfg.PartitionLayout == nil {
		return nil
	}

	layoutDevice := strings.TrimSpace(o.cfg.PartitionLayout.Device)
	o.cfg.PartitionLayout.Device = layoutDevice

	// Check device conflicts before mode compatibility so that
	// configuration errors surface immediately.
	if layoutDevice != "" && o.cfg.DiskDevice != "" && o.cfg.DiskDevice != layoutDevice {
		return fmt.Errorf("disk device conflict: DISK_DEVICE=%q differs from PARTITION_LAYOUT.device=%q", o.cfg.DiskDevice, layoutDevice)
	}

	if err := o.validatePartitionLayoutModeCompatibility(); err != nil {
		return err
	}

	if layoutDevice == "" {
		return nil
	}

	info, err := os.Stat(layoutDevice)
	if err != nil {
		return fmt.Errorf("partition layout device %q: %w", layoutDevice, err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("partition layout device %q is not a block device", layoutDevice)
	}

	return nil
}

func (o *Orchestrator) detectDisk(ctx context.Context) error {
	if o.cfg.PartitionLayout != nil {
		layoutDevice := strings.TrimSpace(o.cfg.PartitionLayout.Device)
		o.cfg.PartitionLayout.Device = layoutDevice
		if layoutDevice != "" {
			o.targetDisk = layoutDevice
			o.log.Info("using partition layout device override", "device", o.targetDisk)
			return nil
		}
	}

	// If a specific disk device is configured, validate and use it directly.
	if o.cfg.DiskDevice != "" {
		info, err := os.Stat(o.cfg.DiskDevice)
		if err != nil {
			return fmt.Errorf("configured disk device %s: %w", o.cfg.DiskDevice, err)
		}
		if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
			return fmt.Errorf("configured disk device %s is not a block device", o.cfg.DiskDevice)
		}
		o.log.Info("Using configured disk device", "device", o.cfg.DiskDevice)
		o.targetDisk = o.cfg.DiskDevice
		return nil
	}
	d, err := o.disk.DetectDisk(ctx, o.cfg.MinDiskSizeGB)
	if err != nil {
		return err
	}
	o.targetDisk = d
	return nil
}

func (o *Orchestrator) applyPartitionLayout(ctx context.Context) error {
	if o.cfg.PartitionLayout == nil {
		return nil
	}

	device := o.targetDisk
	layoutDevice := strings.TrimSpace(o.cfg.PartitionLayout.Device)
	o.cfg.PartitionLayout.Device = layoutDevice
	if layoutDevice != "" {
		device = layoutDevice
		o.targetDisk = device
	}

	// Validate the target device exists and is a block device.
	if info, err := os.Stat(device); err != nil {
		return fmt.Errorf("partition layout device %q: %w", device, err)
	} else if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("partition layout device %q is not a block device", device)
	}

	o.log.Info("applying custom partition layout", "device", device, "partitions", len(o.cfg.PartitionLayout.Partitions))

	if err := o.disk.ApplyPartitionLayout(ctx, device, o.cfg.PartitionLayout); err != nil {
		return fmt.Errorf("apply partition layout: %w", err)
	}

	// Apply LVM if configured.
	if o.cfg.PartitionLayout.LVM != nil {
		if err := o.disk.ApplyLVMConfig(ctx, device, o.cfg.PartitionLayout); err != nil {
			return fmt.Errorf("apply LVM config: %w", err)
		}
	}

	o.log.Info("custom partition layout applied")
	return nil
}

// writeFstab generates and writes fstab after root is mounted.
func (o *Orchestrator) writeFstabStep(_ context.Context) error {
	return o.writeFstab()
}

func (o *Orchestrator) writeFstab() error {
	if o.cfg.PartitionLayout == nil {
		return nil
	}
	device := o.targetDisk
	fstab := disk.GenerateFstab(o.cfg.PartitionLayout, device)
	if o.cfg.PartitionLayout.LVM != nil {
		fstab += disk.GenerateLVMFstab(o.cfg.PartitionLayout.LVM)
	}

	fstabPath := filepath.Join(o.config.rootDir, "etc", "fstab")
	if err := os.MkdirAll(filepath.Dir(fstabPath), 0o755); err != nil {
		return fmt.Errorf("creating fstab directory: %w", err)
	}
	if err := os.WriteFile(fstabPath, []byte(fstab), 0o644); err != nil {
		return fmt.Errorf("writing fstab: %w", err)
	}
	o.log.Info("generated fstab for custom layout")
	return nil
}

func (o *Orchestrator) streamImage(ctx context.Context) error {
	// With a custom partition layout, fail fast — rootfs extraction for
	// layout mode is not implemented yet.
	if o.cfg.PartitionLayout != nil {
		return fmt.Errorf("%s", errPartitionLayoutNotSupported)
	}

	bestURL := o.bestImageURL
	if bestURL == "" {
		// verify-image may have skipped URL resolution; resolve it now.
		var err error
		bestURL, err = image.SelectBestSource(ctx, o.cfg.ImageURLs)
		if err != nil {
			return fmt.Errorf("selecting image source: %w", err)
		}
	}

	// Partition-by-partition mode: download to ramdisk, copy each partition individually.
	if strings.EqualFold(o.cfg.ImageMode, "partition") {
		o.log.Info("Streaming image partition-by-partition", "url", bestURL, "disk", o.targetDisk)
		return image.StreamPartitions(ctx, bestURL, o.targetDisk)
	}

	var opts []image.StreamOpts
	if o.cfg.ImageChecksum != "" {
		opts = append(opts, image.StreamOpts{
			Checksum:     o.cfg.ImageChecksum,
			ChecksumType: o.cfg.ImageChecksumType,
		})
	}

	// Default whole-disk mode.
	o.log.Info("Streaming image", "url", bestURL, "disk", o.targetDisk)
	if err := image.Stream(ctx, bestURL, o.targetDisk, opts...); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "checksum mismatch") {
			return &PermanentError{Err: fmt.Errorf("streaming %s: %w", bestURL, err)}
		}
		return fmt.Errorf("streaming %s: %w", bestURL, err)
	}
	return nil
}

func (o *Orchestrator) verifyImageSignature(ctx context.Context) error {
	// Resolve the best image URL so stream-image reuses the same source.
	// NOTE: This step streams the image content for GPG verification. The
	// subsequent stream-image step downloads the same URL again. This is an
	// intentional tradeoff: signature verification must complete before
	// writing to disk, and piping the same stream into both GPG and the
	// block device would require buffering multi-GB images in memory.
	bestURL, err := image.SelectBestSource(ctx, o.cfg.ImageURLs)
	if err != nil {
		// If signature verification is not configured, URL resolution failures
		// will be caught by stream-image. Don't block provisioning here.
		if o.cfg.ImageSignatureURL == "" {
			o.log.Info("no image signature URL configured, skipping verification")
			return nil
		}
		return fmt.Errorf("selecting image source: %w", err)
	}
	o.bestImageURL = bestURL

	if o.cfg.ImageSignatureURL == "" {
		o.log.Info("no image signature URL configured, skipping verification")
		return nil
	}
	if o.cfg.ImageGPGPubKey == "" {
		return fmt.Errorf("image signature URL set but no GPG public key path configured")
	}

	return image.VerifyGPGSignature(ctx, bestURL, o.cfg.ImageSignatureURL, o.cfg.ImageGPGPubKey)
}

func (o *Orchestrator) partprobe(ctx context.Context) error {
	return o.disk.PartProbe(ctx, o.targetDisk)
}

func (o *Orchestrator) parsePartitions(ctx context.Context) error {
	// With a custom partition layout, derive root from the layout definition
	// rather than scanning by GUID (which can pick the wrong partition when
	// multiple Linux-type partitions exist).
	if o.cfg.PartitionLayout != nil {
		return o.parsePartitionsFromLayout(ctx)
	}

	parts, err := o.disk.ParsePartitions(ctx, o.targetDisk)
	if err != nil {
		return err
	}

	boot, err := o.disk.FindBootPartition(parts)
	if err != nil {
		o.log.Warn("No EFI partition found", "error", err)
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

// parsePartitionsFromLayout determines boot/root partitions from the declared
// partition layout instead of scanning GPT type GUIDs.
func (o *Orchestrator) parsePartitionsFromLayout(_ context.Context) error {
	layout := o.cfg.PartitionLayout

	if err := o.resolveRootFromLayout(layout); err != nil {
		return err
	}

	// Find boot/EFI partition from the layout.
	// Require an explicit /boot/efi mountpoint to avoid choosing the wrong
	// partition in layouts with multiple vfat filesystems.
	espIdx := -1
	for i, part := range layout.Partitions {
		if part.Mountpoint == "/boot/efi" {
			espIdx = i
			break
		}
	}
	if espIdx == -1 {
		o.log.Warn("no /boot/efi mountpoint found in partition layout; efi boot entry creation may be skipped")
		return nil
	}
	o.bootPartition = disk.PartitionDevicePath(o.targetDisk, espIdx+1)
	o.log.Info("boot partition from layout", "device", o.bootPartition)

	return nil
}

func (o *Orchestrator) resolveRootFromLayout(layout *config.PartitionLayout) error {
	if layout == nil {
		return fmt.Errorf("partition layout is nil")
	}

	// When LVM is configured, use the LV with mountpoint "/" as root.
	if layout.LVM != nil {
		for _, vol := range layout.LVM.Volumes {
			if vol.Mountpoint == "/" {
				o.rootPartition = fmt.Sprintf("/dev/%s/%s", layout.LVM.VolumeGroup, vol.Name)
				o.log.Info("root from lvm", "device", o.rootPartition)
				return nil
			}
		}
	}

	// Find the partition with mountpoint "/" from the layout definition.
	for i, part := range layout.Partitions {
		if part.Mountpoint == "/" {
			o.rootPartition = disk.PartitionDevicePath(o.targetDisk, i+1)
			o.log.Info("root from partition layout", "device", o.rootPartition)
			return nil
		}
	}
	return fmt.Errorf("partition layout has no mountpoint \"/\" in partitions or lvm volumes")
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
	if o.cfg.PartitionLayout != nil {
		o.log.Info("skipping grow-partition for declarative partition layout")
		return nil
	}

	partNum := disk.PartitionNumber(o.rootPartition, o.targetDisk)
	if partNum == 0 {
		o.log.Warn("Could not determine partition number, skipping grow")
		return nil
	}
	return o.disk.GrowPartition(ctx, o.targetDisk, partNum)
}

func (o *Orchestrator) resizeFilesystem(ctx context.Context) error {
	if o.cfg.PartitionLayout != nil {
		o.log.Info("skipping resize-filesystem for declarative partition layout")
		return nil
	}

	return o.disk.ResizeFilesystem(ctx, o.rootPartition)
}

func (o *Orchestrator) configureKubelet(_ context.Context) error {
	return o.config.ConfigureKubelet(o.cfg)
}

func (o *Orchestrator) configureGRUB(ctx context.Context) error {
	return o.config.ConfigureGRUB(ctx, o.cfg)
}

func (o *Orchestrator) injectCloudInit(_ context.Context) error {
	if !o.cfg.CloudInitEnabled {
		return nil
	}

	// Validate datasource — only NoCloud is supported.
	ds := strings.ToLower(strings.TrimSpace(o.cfg.CloudInitDatasource))
	if ds == "" {
		ds = "nocloud"
	}
	if ds != "nocloud" {
		return fmt.Errorf("unsupported cloud-init datasource %q, only \"nocloud\" is supported", ds)
	}

	// Split bond interfaces, trimming spaces and filtering empty entries.
	var bondIfaces []string
	for _, iface := range strings.Split(o.cfg.BondInterfaces, ",") {
		if s := strings.TrimSpace(iface); s != "" {
			bondIfaces = append(bondIfaces, s)
		}
	}

	// Parse DNS resolvers, trimming spaces and filtering empty entries.
	var dns []string
	for _, r := range strings.Split(o.cfg.DNSResolvers, ",") {
		if s := strings.TrimSpace(r); s != "" {
			dns = append(dns, s)
		}
	}

	// Cloud-init expects a stable, non-empty instance-id for first-boot identity.
	instanceID := strings.TrimSpace(o.cfg.ProviderID)
	if instanceID == "" {
		instanceID = strings.TrimSpace(o.cfg.Hostname)
	}
	if instanceID == "" {
		instanceID = "booty"
	}

	ciCfg := &cloudinit.Config{
		Hostname:   o.cfg.Hostname,
		InstanceID: instanceID,
		StaticIP:   o.cfg.StaticIP,
		Gateway:    o.cfg.StaticGateway,
		BondIfaces: bondIfaces,
		BondMode:   o.cfg.BondMode,
		DNS:        dns,
	}

	ud, md, nc := cloudinit.Generate(ciCfg)
	rootPath := o.config.rootDir
	if err := cloudinit.InjectNoCloud(rootPath, ud, md, nc); err != nil {
		return fmt.Errorf("inject cloud-init: %w", err)
	}
	o.log.Info("cloud-init nocloud seed injected", "root", rootPath)
	return nil
}

func (o *Orchestrator) copyMachineFiles(ctx context.Context) error {
	return o.config.CopyMachineFiles(ctx)
}

func (o *Orchestrator) runMachineCommands(ctx context.Context) error {
	return o.config.RunMachineCommands(ctx)
}

func (o *Orchestrator) runPostProvisionCmds(ctx context.Context) error {
	if len(o.cfg.PostProvisionCmds) == 0 {
		return nil
	}
	return o.config.RunPostProvisionCmds(ctx, o.cfg.PostProvisionCmds)
}

func (o *Orchestrator) teardownChroot(_ context.Context) error {
	bindErr := o.disk.TeardownChrootBindMounts(newroot)
	unmountErr := o.disk.Unmount(newroot)
	return errors.Join(bindErr, unmountErr)
}

func (o *Orchestrator) runHealthChecks(ctx context.Context) error {
	if !o.cfg.HealthChecksEnabled {
		o.log.Info("Health checks disabled, skipping")
		return nil
	}

	checks := []health.Check{
		&health.DiskPresenceCheck{},
		&health.DiskIOErrorCheck{},
		&health.MemoryECCCheck{},
		&health.MinimumMemoryCheck{MinGiB: o.cfg.HealthMinMemoryGB},
		&health.MinimumCPUCheck{MinCPUs: o.cfg.HealthMinCPUs},
		&health.NICLinkStateCheck{},
		&health.ThermalStateCheck{},
	}

	results, critical := health.RunAll(ctx, checks, o.cfg.HealthSkipChecks)

	for _, r := range results {
		logAttrs := []any{
			"check", r.Name,
			"status", r.Status,
			"severity", r.Severity,
			"message", r.Message,
		}
		if r.Details != "" {
			logAttrs = append(logAttrs, "details", r.Details)
		}
		o.log.Info("Health check result", logAttrs...)
	}

	// Best-effort report to server.
	if reporter, ok := o.provider.(HealthReporter); ok {
		if err := reporter.ReportHealthChecks(ctx, results); err != nil {
			o.log.Warn("Failed to report health checks", "error", err)
		}
	}

	if critical {
		var failed []string
		for _, r := range results {
			if r.Status == health.StatusFail && r.Severity == health.SeverityCritical {
				failed = append(failed, r.Name)
			}
		}
		return fmt.Errorf("critical health check(s) failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

func (o *Orchestrator) reportSuccess(ctx context.Context) error {
	return o.provider.ReportStatus(ctx, config.StatusSuccess, "provisioning complete")
}

// DumpDebugState logs system state useful for diagnosing failures.
// BOOTy runs as PID 1 in an initramfs — this dump is the only diagnostic
// data available before reboot, so it must be comprehensive.
// Step-specific debug commands are run first, followed by comprehensive dump.
// Automatically detects whether FRR (vtysh) or GoBGP is in use and runs
// the appropriate network diagnostics.
func DumpDebugState(failedStep string) {
	slog.Warn("=== DEBUG DUMP START ===", "failedStep", failedStep)

	// PATH and available binaries — critical for diagnosing missing-binary issues.
	executil.DumpPATH()

	// Shared library availability — dynamically-linked tools fail silently without these.
	runDebugCmd("shared libs", "ls -la /lib64/ld-linux-x86-64.so* /lib/ld-linux-x86-64.so* /lib/x86_64-linux-gnu/lib*.so* /usr/lib/x86_64-linux-gnu/lib*.so* /lib64/ld-linux-aarch64.so* /lib/ld-linux-aarch64.so* /lib/aarch64-linux-gnu/lib*.so* /usr/lib/aarch64-linux-gnu/lib*.so* 2>/dev/null | head -40 || echo 'no shared libs found'")
	runDebugCmd("ld.so.cache", "ldconfig -p 2>/dev/null | head -20 || echo 'ldconfig not available'")

	// Step-specific commands run first for targeted diagnostics.
	stepCmds := stepDebugCmds(failedStep)
	for _, dc := range stepCmds {
		runDebugCmd(dc.label, dc.cmd)
	}

	debugCmds := []struct {
		label string
		cmd   string
	}{
		// Block devices & disk subsystem.
		{"block devices", "lsblk -a"},
		{"mounts", "cat /proc/mounts"},
		{"memory", "cat /proc/meminfo"},
		{"disk partitions", "cat /proc/partitions"},
		{"mdstat", "cat /proc/mdstat"},
		{"df", "df -h"},
		{"pvs", "pvs"},
		{"lvs", "lvs"},

		// Kernel messages.
		{"dmesg tail", "dmesg | tail -100"},

		// Network interfaces & routes (IPv4 + IPv6).
		{"network interfaces", "ip -br addr"},
		{"interface stats", "ip -s link"},
		{"routes v4", "ip route"},
		{"routes v6", "ip -6 route"},
		{"bridge fdb", "bridge fdb show"},
		{"vxlan interfaces", "ip link show type vxlan"},
	}

	for _, dc := range debugCmds {
		runDebugCmd(dc.label, dc.cmd)
	}

	// Network mode–specific diagnostics: FRR (vtysh) vs GoBGP (in-process).
	if hasBinary("vtysh") {
		frrDebugCmds(failedStep)
	} else {
		gobgpDebugCmds()
	}

	// Log environment (redact sensitive values).
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "BOOTY_") || strings.HasPrefix(env, "MODE=") ||
			strings.HasPrefix(env, "NETWORK_MODE=") {
			key, _, _ := strings.Cut(env, "=")
			if isSensitiveEnvKey(key) {
				slog.Warn("debug env", "var", key+"=REDACTED")
			} else {
				slog.Warn("debug env", "var", env)
			}
		}
	}

	slog.Warn("=== DEBUG DUMP END ===", "failedStep", failedStep)
}

// hasBinary reports whether the named binary exists in PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// isSensitiveEnvKey returns true if the key likely contains credentials.
func isSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, s := range []string{"TOKEN", "SECRET", "PASSWORD", "KEY", "CREDENTIAL"} {
		if strings.Contains(upper, s) {
			return true
		}
	}
	return false
}

// frrDebugCmds dumps FRR-specific state using vtysh.
func frrDebugCmds(_ string) {
	cmds := []debugCmd{
		{"frr config", "cat /etc/frr/frr.conf"},
		{"frr daemons", "pgrep -la 'bgpd|zebra|bfdd|mgmtd|staticd'"},
		{"frr log tail", "tail -100 /var/log/frr/frr.log"},
		{"bgp summary", "vtysh -c 'show bgp summary'"},
		{"bgp ipv4", "vtysh -c 'show bgp ipv4 unicast'"},
		{"bgp ipv6", "vtysh -c 'show bgp ipv6 unicast'"},
		{"bgp l2vpn evpn", "vtysh -c 'show bgp l2vpn evpn'"},
		{"bfd peers", "vtysh -c 'show bfd peers'"},
		{"frr interfaces", "vtysh -c 'show interface brief'"},
	}
	for _, dc := range cmds {
		runDebugCmd(dc.label, dc.cmd)
	}
}

// gobgpDebugCmds dumps network state available without FRR/vtysh.
// GoBGP runs in-process so there is no CLI to query; instead we dump
// kernel state that reflects what the GoBGP stack has programmed.
func gobgpDebugCmds() {
	slog.Warn("debug", "label", "network-mode", "data", "GoBGP (in-process, no vtysh)")
	cmds := []debugCmd{
		// VRF state — GoBGP programs routes into a VRF.
		{"vrf devices", "ip -d link show type vrf"},
		{"vrf routes", "ip route show vrf provision 2>/dev/null || ip route show table all"},
		{"vrf neighbors", "ip neigh show vrf provision 2>/dev/null || ip neigh show"},
		// VXLAN tunnel state.
		{"vxlan details", "ip -d link show type vxlan"},
		{"vxlan fdb", "bridge fdb show dev vx100 2>/dev/null || true"},
		// Bridge state.
		{"bridge links", "ip -d link show type bridge"},
		{"bridge vlan", "bridge vlan show 2>/dev/null || true"},
		// General neighbor/ARP state.
		{"arp table", "ip neigh show"},
		// Routing tables.
		{"all routes", "ip route show table all 2>/dev/null | head -80"},
	}
	for _, dc := range cmds {
		runDebugCmd(dc.label, dc.cmd)
	}
}

// debugCtx returns a context with a 10-second timeout for debug commands,
// preventing them from blocking shutdown indefinitely.
func debugCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second) //nolint:mnd // fixed debug timeout
}

// runDebugCmd executes a single debug command and logs its output.
func runDebugCmd(label, cmd string) {
	ctx, cancel := debugCtx()
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput() //nolint:gosec // debug cmds are hardcoded
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		for _, line := range strings.Split(trimmed, "\n") {
			if line != "" {
				slog.Warn("debug", "label", label, "data", line)
			}
		}
	}
	if err != nil {
		slog.Warn("debug command failed", "label", label, "cmd", cmd, "error", err)
	}
}

type debugCmd struct {
	label string
	cmd   string
}

// stepDebugCmds returns step-specific debug commands for targeted diagnostics.
func stepDebugCmds(step string) []debugCmd {
	switch step {
	case "detect-disk":
		return []debugCmd{
			{"sysblock entries", "ls -la /sys/block/"},
			{"sysblock sizes", "for d in /sys/block/*/size; do echo \"$d: $(cat $d)\"; done"},
			{"dev devices", "ls -la /dev/sd* /dev/nvme* /dev/vd* /dev/loop* 2>/dev/null || true"},
			{"loaded modules", "lsmod 2>/dev/null || cat /proc/modules | head -30"},
			{"scsi devices", "cat /proc/scsi/scsi 2>/dev/null || echo 'no SCSI info'"},
		}
	case "wipe-disks":
		return []debugCmd{
			{"wipefs version", "wipefs --version 2>&1 || echo 'wipefs not available'"},
			{"sgdisk version", "sgdisk --version 2>&1 || echo 'sgdisk not available'"},
			{"shared libs wipefs", "ldd $(which wipefs 2>/dev/null) 2>&1 || echo 'ldd/wipefs not found'"},
			{"shared libs sgdisk", "ldd $(which sgdisk 2>/dev/null) 2>&1 || echo 'ldd/sgdisk not found'"},
			{"ld.so check", "ls -la /lib64/ld-linux-x86-64.so.2 /lib/ld-linux-x86-64.so.2 /lib64/ld-linux-aarch64.so.1 /lib/ld-linux-aarch64.so.1 2>/dev/null || echo 'dynamic linker not found'"},
			{"dev devices", "ls -la /dev/sd* /dev/nvme* /dev/vd* 2>/dev/null || true"},
		}
	case "parse-partitions", "apply-partition-layout":
		return []debugCmd{
			{"sfdisk version", "sfdisk --version 2>&1 || echo 'sfdisk not found'"},
			{"sfdisk raw", "for d in /dev/sd[a-z] /dev/nvme*n1 /dev/vd[a-z]; do if [ -b \"$d\" ]; then sfdisk --json \"$d\"; break; fi; done 2>&1 | head -30 || true"},
			{"fdisk list", "fdisk -l 2>/dev/null | head -40 || true"},
			{"partitions", "cat /proc/partitions"},
			{"shared libs sfdisk", "ldd $(which sfdisk) 2>&1 || echo 'ldd not found'"},
		}
	case "stream-image":
		return []debugCmd{
			{"target disk info", "fdisk -l 2>/dev/null | head -30 || true"},
			{"disk space", "df -h"},
			{"partitions", "cat /proc/partitions"},
		}
	case "mount-root", "setup-chroot-binds":
		return []debugCmd{
			{"proc mounts", "cat /proc/mounts"},
			{"newroot contents", "ls -la /newroot/ 2>/dev/null || echo '/newroot not found'"},
		}
	case "configure-grub", "run-machine-commands", "run-post-provision-cmds", "configure-kubelet":
		return []debugCmd{
			{"chroot bin", "ls /newroot/bin/ /newroot/usr/bin/ 2>/dev/null | head -50 || true"},
			{"chroot boot", "ls -la /newroot/boot/ 2>/dev/null || echo '/newroot/boot not found'"},
			{"chroot mounts", "cat /proc/mounts | grep newroot || true"},
		}
	case "remove-efi-entries", "create-efi-boot-entry", "mount-efivarfs":
		return []debugCmd{
			{"efivarfs", "ls /sys/firmware/efi/efivars/ 2>/dev/null | head -20 || echo 'no EFI'"},
			{"efibootmgr", "efibootmgr -v 2>/dev/null || echo 'efibootmgr not available'"},
			{"proc mounts efi", "grep efi /proc/mounts 2>/dev/null || echo 'no efi mounts'"},
		}
	default:
		return nil
	}
}

// dumpConfig logs the parsed machine configuration on failure.
// Token is excluded to avoid leaking credentials.
func dumpConfig(cfg *config.MachineConfig) {
	if cfg == nil {
		return
	}
	slog.Warn("=== CONFIG DUMP ===",
		"hostname", cfg.Hostname,
		"mode", cfg.Mode,
		"images", redactURLs(cfg.ImageURLs),
		"asn", cfg.ASN,
		"provision_vni", cfg.ProvisionVNI,
		"underlay_subnet", cfg.UnderlaySubnet,
		"underlay_ip", cfg.UnderlayIP,
		"overlay_subnet", cfg.OverlaySubnet,
		"ipmi_subnet", cfg.IPMISubnet,
		"dcgw_ips", cfg.DCGWIPs,
		"leaf_asn", cfg.LeafASN,
		"local_asn", cfg.LocalASN,
		"vpn_rt", cfg.VPNRT,
		"overlay_aggregate", cfg.OverlayAggregate,
		"provision_ip", cfg.ProvisionIP,
		"dns_resolver", cfg.DNSResolvers,
		"vrf_table_id", cfg.VRFTableID,
		"bgp_keepalive", cfg.BGPKeepalive,
		"bgp_hold", cfg.BGPHold,
		"bfd_transmit_ms", cfg.BFDTransmitMS,
		"bfd_receive_ms", cfg.BFDReceiveMS,
		"static_ip", cfg.StaticIP,
		"static_gateway", cfg.StaticGateway,
		"bond_interfaces", cfg.BondInterfaces,
		"min_disk_size_gb", cfg.MinDiskSizeGB,
	)
}

// redactURLs strips embedded credentials from image URLs to prevent leaking
// secrets (e.g. oci://user:pass@registry/image:tag) in debug logs.
func redactURLs(urls []string) []string {
	redacted := make([]string, len(urls))
	for i, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			redacted[i] = raw
			continue
		}
		u.User = url.User("REDACTED")
		redacted[i] = u.String()
	}
	return redacted
}
