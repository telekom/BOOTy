//go:build linux

package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/firmware"
	"github.com/telekom/BOOTy/pkg/health"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/inventory"
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
	firmwareChanged bool // true if any step changed firmware values requiring hard reboot
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

// Provision runs all provisioning steps sequentially.
func (o *Orchestrator) Provision(ctx context.Context) error {
	steps := []Step{
		{"report-init", o.reportInit},
		{"collect-inventory", o.collectInventory},
		{"collect-firmware", o.collectFirmware},
		{"health-checks", o.runHealthChecks},
		{"set-hostname", o.setHostname},
		{"copy-provisioner-files", o.copyProvisionerFiles},
		{"configure-dns", o.configureDNS},
		{"stop-raid", o.stopRAID},
		{"disable-lvm", o.disableLVM},
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
		{"run-post-provision-cmds", o.runPostProvisionCmds},
		{"create-efi-boot-entry", o.createEFIBootEntry},
		{"teardown-chroot", o.teardownChroot},
		{"report-success", o.reportSuccess},
	}

	for i, step := range steps {
		o.log.Info("Provisioning step", "step", step.Name, "index", i+1, "total", len(steps))
		if err := step.Fn(ctx); err != nil {
			msg := fmt.Sprintf("step %s failed: %v", step.Name, err)
			o.log.Error("Provisioning step failed", "step", step.Name, "error", err)
			DumpDebugState(step.Name)
			dumpConfig(o.cfg)
			_ = o.provider.ReportStatus(ctx, config.StatusError, msg)
			return fmt.Errorf("provision step %s: %w", step.Name, err)
		}
	}
	return nil
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

func (o *Orchestrator) copyProvisionerFiles(_ context.Context) error {
	return o.config.CopyProvisionerFiles()
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

func (o *Orchestrator) wipeDisks(ctx context.Context) error {
	return o.disk.WipeAllDisks(ctx)
}

func (o *Orchestrator) secureEraseDisks(ctx context.Context) error {
	return o.disk.SecureEraseAllDisks(ctx)
}

func (o *Orchestrator) detectDisk(ctx context.Context) error {
	// If a specific disk device is configured, validate and use it directly.
	if o.cfg.DiskDevice != "" {
		info, err := os.Stat(o.cfg.DiskDevice)
		if err != nil {
			return fmt.Errorf("configured disk device %s: %w", o.cfg.DiskDevice, err)
		}
		if info.Mode()&os.ModeDevice == 0 {
			return fmt.Errorf("configured disk device %s is not a device node", o.cfg.DiskDevice)
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

func (o *Orchestrator) streamImage(ctx context.Context) error {
	var opts []image.StreamOpts
	if o.cfg.ImageChecksum != "" {
		opts = append(opts, image.StreamOpts{
			Checksum:     o.cfg.ImageChecksum,
			ChecksumType: o.cfg.ImageChecksumType,
		})
	}
	for _, imgURL := range o.cfg.ImageURLs {
		o.log.Info("Streaming image", "url", imgURL, "disk", o.targetDisk)
		if err := image.Stream(ctx, imgURL, o.targetDisk, opts...); err != nil {
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
		o.log.Warn("Could not determine partition number, skipping grow")
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

func (o *Orchestrator) runPostProvisionCmds(ctx context.Context) error {
	if len(o.cfg.PostProvisionCmds) == 0 {
		return nil
	}
	return o.config.RunPostProvisionCmds(ctx, o.cfg.PostProvisionCmds)
}

func (o *Orchestrator) teardownChroot(_ context.Context) error {
	o.disk.TeardownChrootBindMounts(newroot)
	return o.disk.Unmount(newroot)
}

func (o *Orchestrator) runHealthChecks(ctx context.Context) error {
	if !o.cfg.HealthChecksEnabled {
		o.log.Info("Health checks disabled, skipping")
		return nil
	}

	checks := []health.Check{
		&health.DiskPresenceCheck{},
		&health.DiskSMARTCheck{},
		&health.MemoryECCCheck{},
		&health.MinimumMemoryCheck{MinGB: o.cfg.HealthMinMemoryGB},
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
func DumpDebugState(failedStep string) {
	slog.Error("=== DEBUG DUMP START ===", "failedStep", failedStep)

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

		// FRR state.
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

	for _, dc := range debugCmds {
		runDebugCmd(dc.label, dc.cmd)
	}

	// Log environment.
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "BOOTY_") || strings.HasPrefix(env, "MODE=") {
			slog.Error("DEBUG env", "var", env)
		}
	}

	slog.Error("=== DEBUG DUMP END ===", "failedStep", failedStep)
}

// runDebugCmd executes a single debug command and logs its output.
func runDebugCmd(label, cmd string) {
	out, err := exec.CommandContext(context.Background(), "sh", "-c", cmd).CombinedOutput() //nolint:gosec // debug cmds are hardcoded
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		for _, line := range strings.Split(trimmed, "\n") {
			if line != "" {
				slog.Error("DEBUG", "label", label, "data", line)
			}
		}
	}
	if err != nil {
		slog.Error("Debug command failed", "label", label, "cmd", cmd, "error", err)
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
	case "remove-efi-entries", "create-efi-boot-entry":
		return []debugCmd{
			{"efivarfs", "ls /sys/firmware/efi/efivars/ 2>/dev/null | head -20 || echo 'no EFI'"},
			{"efibootmgr", "efibootmgr -v 2>/dev/null || echo 'efibootmgr not available'"},
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
	slog.Error("=== CONFIG DUMP ===",
		"hostname", cfg.Hostname,
		"mode", cfg.Mode,
		"images", cfg.ImageURLs,
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
