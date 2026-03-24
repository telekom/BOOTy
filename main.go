//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/kexec"
	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/network/frr"
	"github.com/telekom/BOOTy/pkg/network/gobgp"
	"github.com/telekom/BOOTy/pkg/network/netplan"
	"github.com/telekom/BOOTy/pkg/network/vlan"
	"github.com/telekom/BOOTy/pkg/provision"
	"github.com/telekom/BOOTy/pkg/rescue"
	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/realm"
	"github.com/telekom/BOOTy/pkg/ux"
)

// Version and Build are set via -ldflags at build time.
var (
	Version = "dev"
	Build   = "unknown"
)

const varsPath = "/deploy/vars"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// Ensure PATH includes standard binary directories. As PID 1 in an
	// initramfs the kernel default may only contain /sbin:/bin; make sure
	// /usr/bin, /usr/sbin, and /usr/local/bin are also reachable.
	ensurePATH("/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin")

	setupMountsAndDevices()
	loadModules()

	slog.Info("Starting BOOTy", "version", Version, "build", Build)
	ux.Captain()
	ux.SysInfo()

	slog.Info("Beginning provisioning process")
	ctx := context.Background()
	runCAPRF(ctx)
}

// ensurePATH adds each dir to PATH if not already present, preserving any
// directories the build environment or initramfs may have set.
func ensurePATH(dirs ...string) {
	existing := os.Getenv("PATH")
	have := make(map[string]bool)
	for _, d := range strings.Split(existing, ":") {
		have[d] = true
	}
	for _, d := range dirs {
		if !have[d] {
			if existing != "" {
				existing += ":"
			}
			existing += d
			have[d] = true
		}
	}
	if err := os.Setenv("PATH", existing); err != nil {
		slog.Warn("failed to set PATH", "error", err)
	}
}

// setupMountsAndDevices performs early init: mount filesystems and create devices.
func setupMountsAndDevices() {
	m := realm.DefaultMounts()
	d := realm.DefaultDevices()

	for _, name := range []string{"dev", "proc", "tmp", "sys"} {
		mt := m.GetMount(name)
		mt.CreateMount = true
		mt.EnableMount = true
	}

	if err := m.CreateFolder(); err != nil {
		slog.Error("Failed to create folders", "error", err)
	}
	if err := m.MountNamed("dev", true); err != nil {
		slog.Error("Failed to mount dev", "error", err)
	}
	if err := d.CreateDevice(); err != nil {
		slog.Error("Failed to create devices", "error", err)
	}
	if err := m.MountAll(); err != nil {
		slog.Error("Failed to mount filesystems", "error", err)
	}
}

// loadModules loads kernel modules from /modules/ for server NICs and storage controllers.
// Uses the finit_module syscall directly instead of shelling out to insmod.
// Errors are non-fatal: modules may already be built-in or not needed.
//
// Module dependencies are resolved by retrying: modules that fail on the
// first pass (e.g. virtio_net depends on virtio_pci → virtio → virtio_ring)
// succeed on subsequent passes once their dependencies have been loaded.
func loadModules() {
	const moduleDir = "/modules"
	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		slog.Debug("No kernel modules directory, skipping", "path", moduleDir)
		return
	}

	// Collect all module paths.
	var pending []string
	for _, entry := range entries {
		if !entry.IsDir() {
			pending = append(pending, entry.Name())
		}
	}

	// Retry up to 5 passes to resolve dependency ordering.
	const maxPasses = 5
	for pass := range maxPasses {
		var failed []string
		for _, name := range pending {
			ko := filepath.Join(moduleDir, name)
			if err := loadModule(ko); err != nil {
				failed = append(failed, name)
				if pass == maxPasses-1 {
					slog.Debug("Module load skipped", "module", name, "error", err)
				}
				continue
			}
			slog.Info("Loaded kernel module", "module", name)
		}
		if len(failed) == 0 {
			break
		}
		pending = failed
	}
}

// loadModule loads a single kernel module via the finit_module syscall.
// Returns nil when the module is already loaded or built-in (EEXIST).
func loadModule(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open module %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := unix.FinitModule(int(f.Fd()), "", 0); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil
		}
		return fmt.Errorf("finit_module %s: %w", filepath.Base(path), err)
	}
	return nil
}

// runCAPRF runs the CAPRF provisioning flow (ISO-based, /deploy/vars config).
func runCAPRF(ctx context.Context) {
	// Handle SIGTERM/SIGINT for graceful shutdown.
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	client, err := caprf.New(varsPath)
	if err != nil {
		slog.Error("Failed to create CAPRF client", "error", err)
		provision.DumpDebugState("caprf-init")
		realm.Reboot()
	}

	cfg, err := client.GetConfig(ctx)
	if err != nil {
		slog.Error("Failed to get CAPRF config", "error", err)
		provision.DumpDebugState("config-fetch")
		realm.Reboot()
	}

	// DRY_RUN=true overrides mode before logging.
	if cfg.DryRun {
		cfg.Mode = "dry-run"
	}

	// Wire remote log shipping.
	if cfg.LogURL != "" {
		remote := caprf.NewRemoteHandler(client, slog.Default().Handler(), slog.LevelInfo, 256)
		defer remote.Close()
		slog.SetDefault(slog.New(remote))
	}

	slog.Info("CAPRF mode active",
		"hostname", cfg.Hostname,
		"mode", cfg.Mode,
		"image_count", len(cfg.ImageURLs),
	)

	// Set up networking with retry — if connectivity fails, teardown and
	// rebuild the entire network stack before giving up.
	netMode := setupNetworkMode(ctx, cfg)
	connectivityTarget := cfg.InitURL
	if connectivityTarget == "" {
		connectivityTarget = cfg.SuccessURL
	}
	if connectivityTarget != "" {
		if err := ensureNetworkConnectivity(ctx, cfg, netMode, connectivityTarget); err != nil {
			realm.Reboot()
		}
	}

	// Run provisioning, deprovisioning, or standby based on mode.
	diskMgr := disk.NewManager(nil)
	orch := provision.NewOrchestrator(cfg, client, diskMgr)

	provisionSucceeded := false

	switch cfg.Mode {
	case "standby":
		runStandby(ctx, client, cfg, netMode, diskMgr)
		return // standby handles its own lifecycle
	case "dry-run":
		cfg.DisableKexec = true
		if err := orch.DryRun(ctx); err != nil {
			slog.Error("dry-run failed", "error", err)
		}
		// Do not return here - continue with the normal lifecycle
		// (network teardown, reboot) so PID 1 exits cleanly.
	case "deprovision", "soft-deprovision":
		if cfg.Mode == "soft-deprovision" {
			cfg.Mode = "soft"
		}
		if err := orch.Deprovision(ctx); err != nil {
			slog.Error("Deprovisioning failed", "error", err)
		}
	default:
		var retryState rescue.RetryState
		rescueCfg := orch.RescueConfig()
		for {
			err := orch.Provision(ctx)
			if err == nil {
				provisionSucceeded = true
				break
			}
			slog.Error("Provisioning failed", "error", err)
			if setupErr := rescue.Setup(ctx, rescueCfg); setupErr != nil {
				slog.Warn("Rescue setup error", "error", setupErr)
			}
			action := rescue.Decide(rescueCfg, &retryState)
			slog.Info("Rescue action", "type", action.Type, "message", action.Message)
			switch action.Type {
			case rescue.ModeRetry:
				retryState.RecordAttempt(err)
				slog.Info("Retrying provisioning", "attempt", retryState.Attempts, "delay", rescueCfg.RetryDelay)
				if !sleepWithContext(ctx, rescueCfg.RetryDelay) {
					slog.Info("Retry sleep canceled by context")
					if err := netMode.Teardown(ctx); err != nil {
						slog.Warn("Network teardown error", "error", err)
					}
					realm.Reboot()
					return
				}
				continue
			case rescue.ModeShell:
				slog.Info("Dropping to rescue shell")
				realm.Shell()
			case rescue.ModeWait:
				slog.Info("Waiting for manual intervention")
				<-ctx.Done()
				slog.Info("Context canceled while waiting in rescue mode")
				if err := netMode.Teardown(ctx); err != nil {
					slog.Warn("Network teardown error", "error", err)
				}
				realm.Reboot()
				return
			default:
				// ModeReboot: fall through to reboot
			}
			break
		}
	}

	slog.Info("BOOTy CAPRF complete")
	if err := netMode.Teardown(ctx); err != nil {
		slog.Warn("Network teardown error", "error", err)
	}

	// Attempt kexec into installed kernel; fall back to normal reboot.
	if cfg.Mode != "deprovision" && cfg.Mode != "soft" && provisionSucceeded {
		tryKexec(cfg, orch.FirmwareChanged())
	}
	time.Sleep(time.Second * 2)
	realm.Reboot()
}

// ensureNetworkConnectivity retries network setup up to 3 times on connectivity failure.
// Returns error only if all retries exhausted (for caller to decide reboot behavior).
func ensureNetworkConnectivity(ctx context.Context, cfg *config.MachineConfig, netMode network.Mode, target string) error {
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		slog.Info("Waiting for network connectivity", "target", target, "attempt", attempt)
		if err := netMode.WaitForConnectivity(ctx, target, 5*time.Minute); err == nil {
			slog.Info("Network connectivity established", "target", target)
			return nil
		}
		slog.Error("Network connectivity timeout", "attempt", attempt)
		if attempt < maxRetries {
			slog.Info("Tearing down network for retry", "attempt", attempt)
			if tErr := netMode.Teardown(ctx); tErr != nil {
				slog.Warn("Network teardown failed", "error", tErr)
			}
			netMode = setupNetworkMode(ctx, cfg)
		}
	}
	slog.Error("Network connectivity failed after all retries", "attempts", maxRetries)
	return fmt.Errorf("network connectivity timeout after %d attempts", maxRetries)
}

// setupNetworkMode detects and configures the appropriate network mode.
func setupNetworkMode(ctx context.Context, cfg *config.MachineConfig) network.Mode {
	netCfg := &network.Config{
		UnderlaySubnet:   cfg.UnderlaySubnet,
		UnderlayIP:       cfg.UnderlayIP,
		OverlaySubnet:    cfg.OverlaySubnet,
		IPMISubnet:       cfg.IPMISubnet,
		ASN:              cfg.ASN,
		ProvisionVNI:     cfg.ProvisionVNI,
		ProvisionIP:      cfg.ProvisionIP,
		ProvisionGateway: cfg.ProvisionGateway,
		DNSResolvers:     cfg.DNSResolvers,
		DCGWIPs:          cfg.DCGWIPs,
		LeafASN:          cfg.LeafASN,
		LocalASN:         cfg.LocalASN,
		OverlayAggregate: cfg.OverlayAggregate,
		VPNRT:            cfg.VPNRT,
		StaticIP:         cfg.StaticIP,
		StaticGateway:    cfg.StaticGateway,
		StaticIface:      cfg.StaticIface,
		BondInterfaces:   cfg.BondInterfaces,
		BondMode:         cfg.BondMode,
		VRFTableID:       cfg.VRFTableID,
		BGPKeepalive:     cfg.BGPKeepalive,
		BGPHold:          cfg.BGPHold,
		BFDTransmitMS:    cfg.BFDTransmitMS,
		BFDReceiveMS:     cfg.BFDReceiveMS,
		NetworkMode:      cfg.NetworkMode,
		BGPPeerMode:      network.ParsePeerMode(cfg.BGPPeerMode),
		BGPNeighbors:     cfg.BGPNeighbors,
		BGPRemoteASN:     cfg.BGPRemoteASN,
		EVPNL2Enabled:    cfg.EVPNL2Enabled,
	}

	// Auto-detect netplan configuration files injected by the provisioner.
	// Netplan-derived values override vars-based values, allowing the same
	// config format used by the old deployer to work as a drop-in with BOOTy.
	if npCfg := detectNetplanConfig(); npCfg != nil {
		mergeNetplanConfig(netCfg, npCfg)
	}

	// Parse VLAN configuration.
	if cfg.VLANs != "" {
		vlans, err := network.ParseVLANs(cfg.VLANs)
		if err != nil {
			slog.Error("Invalid VLAN configuration", "error", err)
		} else {
			netCfg.VLANs = vlans
		}
	}

	// Set up VLANs first — they create sub-interfaces that other modes use.
	if netCfg.IsVLANMode() {
		slog.Info("Setting up VLAN interfaces", "count", len(netCfg.VLANs))
		for _, v := range netCfg.VLANs {
			name, err := vlan.Setup(&vlan.Config{
				ID:      v.ID,
				Parent:  v.Parent,
				Address: v.Address,
				Gateway: v.Gateway,
			})
			if err != nil {
				slog.Error("VLAN setup failed", "vlan", v.ID, "parent", v.Parent, "error", err)
				continue
			}
			// If static interface is not set, use the first VLAN interface.
			if netCfg.StaticIface == "" {
				netCfg.StaticIface = name
			}
		}
	}

	// Set up bonding if configured (bond becomes the interface for other modes).
	if netCfg.IsBondMode() {
		slog.Info("Setting up LACP bond")
		bond := &network.BondMode{}
		if err := bond.Setup(ctx, netCfg); err != nil {
			slog.Error("Bond setup failed", "error", err)
		} else if netCfg.StaticIface == "" {
			netCfg.StaticIface = "bond0"
		}
	}

	// Priority: GoBGP > FRR > Static > DHCP.
	if netCfg.IsGoBGPMode() {
		detectIPMI(netCfg)
		slog.Info("Using GoBGP/EVPN network mode", "asn", cfg.ASN)
		stack, err := setupGoBGPStack(ctx, netCfg)
		if err != nil {
			slog.Error("GoBGP setup failed, falling back to FRR", "error", err)
			mgr := frr.NewManager(nil)
			if frrErr := mgr.Setup(ctx, netCfg); frrErr != nil {
				slog.Error("FRR fallback also failed", "error", frrErr)
				mgr.DumpFRRState()
				return dhcpFallback(ctx, netCfg)
			}
			return mgr
		}
		return stack
	}

	if netCfg.IsFRRMode() {
		detectIPMI(netCfg)
		slog.Info("Using FRR/EVPN network mode", "asn", cfg.ASN)
		mgr := frr.NewManager(nil)
		if err := mgr.Setup(ctx, netCfg); err != nil {
			slog.Error("FRR network setup failed, falling back to DHCP", "error", err)
			mgr.DumpFRRState()
			return dhcpFallback(ctx, netCfg)
		}
		return mgr
	}

	if netCfg.IsStaticMode() {
		slog.Info("Using static network mode", "ip", cfg.StaticIP)
		mode := &network.StaticMode{}
		if err := mode.Setup(ctx, netCfg); err != nil {
			slog.Error("Static network setup failed, falling back to DHCP", "error", err)
			return dhcpFallback(ctx, netCfg)
		}
		return mode
	}

	slog.Info("Using DHCP network mode")
	return dhcpFallback(ctx, netCfg)
}

// dhcpFallback creates a DHCP mode and attempts setup.
// Returns the mode even if setup fails, so the caller can still proceed.
func dhcpFallback(ctx context.Context, netCfg *network.Config) network.Mode {
	dhcp := network.NewDHCPMode()
	if err := dhcp.Setup(ctx, netCfg); err != nil {
		slog.Error("DHCP setup failed", "error", err)
	}
	return dhcp
}

// detectNetplanConfig checks for netplan YAML files in the provisioner's
// file-system overlay. If found, it parses them (and any FRR config) into
// a network.Config. Returns nil if no netplan files are present.
func detectNetplanConfig() *network.Config {
	const netplanDir = "/deploy/file-system/etc/netplan"
	const frrConfPath = "/deploy/file-system/etc/frr/frr.conf"

	if !netplan.HasNetplanFiles(netplanDir) {
		return nil
	}
	slog.Info("detected netplan configuration files", "dir", netplanDir)

	np, err := netplan.ParseDir(netplanDir)
	if err != nil {
		slog.Warn("failed to parse netplan files, falling back to vars", "error", err)
		return nil
	}

	var frrParams *netplan.FRRParams
	if data, frrErr := os.ReadFile(frrConfPath); frrErr == nil {
		parsed, parseErr := netplan.ParseFRRConfigBytes(data)
		if parseErr != nil {
			slog.Warn("failed to parse FRR config", "error", parseErr)
		} else {
			frrParams = parsed
			slog.Info("parsed FRR configuration", "asn", frrParams.ASN, "evpn", frrParams.EVPN)
		}
	}

	cfg := netplan.ToNetworkConfig(np, frrParams)
	slog.Info("netplan config loaded",
		"mode", cfg.NetworkMode, "asn", cfg.ASN,
		"vni", cfg.ProvisionVNI, "underlay", cfg.UnderlayIP,
	)
	return cfg
}

// mergeNetplanConfig overrides dst fields with values from the netplan-derived
// src config. Fields that netplan doesn't provide (zero/empty) are left
// unchanged so vars-based operational parameters are preserved.
func mergeNetplanConfig(dst, src *network.Config) {
	if src.ASN > 0 {
		dst.ASN = src.ASN
	}
	if src.ProvisionVNI > 0 {
		dst.ProvisionVNI = src.ProvisionVNI
	}
	if src.UnderlayIP != "" {
		dst.UnderlayIP = src.UnderlayIP
	}
	if src.ProvisionIP != "" {
		dst.ProvisionIP = src.ProvisionIP
	}
	if src.NetworkMode != "" {
		dst.NetworkMode = src.NetworkMode
	}
	if src.BGPPeerMode != "" {
		dst.BGPPeerMode = src.BGPPeerMode
	}
	if src.BGPNeighbors != "" {
		dst.BGPNeighbors = src.BGPNeighbors
	}
	if src.EVPNL2Enabled {
		dst.EVPNL2Enabled = true
	}
	if src.BondInterfaces != "" {
		dst.BondInterfaces = src.BondInterfaces
	}
	if src.BondMode != "" {
		dst.BondMode = src.BondMode
	}
	if src.VRFTableID > 0 {
		dst.VRFTableID = src.VRFTableID
	}
	if src.VRFName != "" {
		dst.VRFName = src.VRFName
	}
	if src.MTU > 0 {
		dst.MTU = src.MTU
	}
	if src.DNSResolvers != "" {
		dst.DNSResolvers = src.DNSResolvers
	}
	if src.StaticGateway != "" {
		dst.StaticGateway = src.StaticGateway
	}
	if len(src.VLANs) > 0 {
		dst.VLANs = src.VLANs
	}
	if len(src.Interfaces) > 0 {
		dst.Interfaces = src.Interfaces
	}
}

// setupGoBGPStack creates and sets up a GoBGP/EVPN network stack.
func setupGoBGPStack(ctx context.Context, netCfg *network.Config) (*gobgp.Stack, error) {
	bgpCfg, err := gobgp.NewConfig(netCfg)
	if err != nil {
		return nil, fmt.Errorf("gobgp config: %w", err)
	}
	stack := gobgp.NewStack(bgpCfg)
	if err := stack.Setup(ctx, netCfg); err != nil {
		// Clean up partially created network state before returning.
		_ = stack.Teardown(ctx)
		return nil, fmt.Errorf("gobgp setup: %w", err)
	}
	return stack, nil
}

// detectIPMI auto-detects IPMI MAC/IP from system if not provided.
func detectIPMI(netCfg *network.Config) {
	if netCfg.IPMIMAC != "" && netCfg.IPMIIP != "" {
		return
	}
	mac, ip, err := network.GetIPMIInfo()
	if err != nil {
		slog.Warn("Failed to detect IPMI info", "error", err)
		return
	}
	if netCfg.IPMIMAC == "" {
		netCfg.IPMIMAC = mac
	}
	if netCfg.IPMIIP == "" {
		netCfg.IPMIIP = ip
	}
	slog.Info("Detected IPMI info", "mac", mac, "ip", ip)
}

// tryKexec attempts to kexec into the installed kernel.
// Falls back silently on failure so the caller can do a normal reboot.
// Skips kexec when disabled by config toggle or when firmware was changed during
// provisioning (e.g. Mellanox SR-IOV), since firmware reinit requires a hard reboot.
func tryKexec(cfg *config.MachineConfig, firmwareChanged bool) {
	if cfg.DisableKexec {
		slog.Info("Kexec disabled by configuration, skipping")
		return
	}

	if firmwareChanged {
		slog.Info("Firmware values changed during provisioning, hard reboot required — skipping kexec")
		return
	}

	const grubPath = "/newroot/boot/grub/grub.cfg"
	f, err := os.Open(grubPath)
	if err != nil {
		slog.Warn("Cannot open grub.cfg, skipping kexec", "error", err)
		return
	}
	defer func() { _ = f.Close() }()

	entries, err := kexec.ParseGrubCfg(f)
	if err != nil {
		slog.Warn("Failed to parse grub.cfg", "error", err)
		return
	}
	entry, err := kexec.GetDefaultEntry(entries)
	if err != nil {
		slog.Warn("No default boot entry found", "error", err)
		return
	}

	kernel := "/newroot" + entry.Kernel
	initrd := "/newroot" + entry.Initramfs
	slog.Info("Attempting kexec", "kernel", kernel, "initrd", initrd)

	if err := kexec.Load(kernel, initrd, entry.KernelArgs); err != nil {
		slog.Warn("kexec load failed, falling back to reboot", "error", err)
		return
	}
	if err := kexec.Execute(); err != nil {
		slog.Warn("kexec execute failed, falling back to reboot", "error", err)
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// runStandby keeps the machine in a hot standby loop. It sends periodic
// heartbeats to the CAPRF server and polls for commands. When a "provision"
// or "deprovision" command arrives, it re-enters the normal CAPRF flow.
func runStandby(ctx context.Context, client config.Provider, cfg *config.MachineConfig, netMode network.Mode, diskMgr *disk.Manager) {
	const (
		heartbeatInterval = 30 * time.Second
		pollInterval      = 10 * time.Second
	)

	slog.Info("Entering standby mode")
	_ = client.ReportStatus(ctx, config.StatusInit, "standby")

	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Standby context canceled, shutting down")
			if err := netMode.Teardown(ctx); err != nil {
				slog.Warn("Network teardown error", "error", err)
			}
			realm.Reboot()
			return

		case <-heartbeatTicker.C:
			if err := client.Heartbeat(ctx); err != nil {
				slog.Warn("Heartbeat failed", "error", err)
			}

		case <-pollTicker.C:
			cmds, err := client.FetchCommands(ctx)
			if err != nil {
				slog.Warn("Command poll failed", "error", err)
				continue
			}
			for _, cmd := range cmds {
				slog.Info("Received command", "id", cmd.ID, "type", cmd.Type)
				switch cmd.Type {
				case "provision":
					cfg.Mode = "provision"
					orch := provision.NewOrchestrator(cfg, client, diskMgr)
					rescueCfg := orch.RescueConfig()
					var retryState rescue.RetryState
					var provErr error
					provisionSucceeded := false
					for {
						provErr = orch.Provision(ctx)
						if provErr == nil {
							provisionSucceeded = true
							break
						}
						slog.Error("Hot provision failed", "error", provErr)
						if setupErr := rescue.Setup(ctx, rescueCfg); setupErr != nil {
							slog.Warn("Rescue setup error", "error", setupErr)
						}
						action := rescue.Decide(rescueCfg, &retryState)
						slog.Info("Rescue action", "type", action.Type, "message", action.Message)
						switch action.Type {
						case rescue.ModeRetry:
							retryState.RecordAttempt(provErr)
							if !sleepWithContext(ctx, rescueCfg.RetryDelay) {
								slog.Info("Retry sleep canceled by context")
								if err := netMode.Teardown(ctx); err != nil {
									slog.Warn("Network teardown error", "error", err)
								}
								realm.Reboot()
								return
							}
							continue
						case rescue.ModeShell:
							slog.Info("Dropping to rescue shell")
							realm.Shell()
							if err := netMode.Teardown(ctx); err != nil {
								slog.Warn("Network teardown error", "error", err)
							}
							realm.Reboot()
							return
						case rescue.ModeWait:
							slog.Info("Waiting for manual intervention")
							<-ctx.Done()
							slog.Info("Standby context canceled while waiting in rescue mode")
							if err := netMode.Teardown(ctx); err != nil {
								slog.Warn("Network teardown error", "error", err)
							}
							realm.Reboot()
							return
						default:
							// ModeReboot
						}
						break
					}
					if !provisionSucceeded {
						if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "failed", provErr.Error()); ackErr != nil {
							slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
						}
						if err := netMode.Teardown(ctx); err != nil {
							slog.Warn("Network teardown error", "error", err)
						}
						realm.Reboot()
						return
					}
					if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "completed", ""); ackErr != nil {
						slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
					}
					if err := netMode.Teardown(ctx); err != nil {
						slog.Warn("Network teardown error", "error", err)
					}
					tryKexec(cfg, orch.FirmwareChanged())
					time.Sleep(2 * time.Second)
					realm.Reboot()
					return
				case "deprovision":
					cfg.Mode = "deprovision"
					orch := provision.NewOrchestrator(cfg, client, diskMgr)
					if err := orch.Deprovision(ctx); err != nil {
						slog.Error("Hot deprovision failed", "error", err)
						if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "failed", err.Error()); ackErr != nil {
							slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
						}
					} else {
						if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "completed", ""); ackErr != nil {
							slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
						}
					}
					if err := netMode.Teardown(ctx); err != nil {
						slog.Warn("Network teardown error", "error", err)
					}
					time.Sleep(2 * time.Second)
					realm.Reboot()
					return
				case "reboot":
					slog.Info("Reboot command received")
					if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "completed", ""); ackErr != nil {
						slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
					}
					if err := netMode.Teardown(ctx); err != nil {
						slog.Warn("Network teardown error", "error", err)
					}
					realm.Reboot()
					return
				case "health-check":
					// Liveness probe — confirms agent is responsive.
					slog.Info("Health-check command received")
					if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "completed", "healthy"); ackErr != nil {
						slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
					}
				default:
					slog.Warn("Unknown command type", "type", cmd.Type)
					if ackErr := client.AcknowledgeCommand(ctx, cmd.ID, "failed", "unknown command type"); ackErr != nil {
						slog.Warn("Failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
					}
				}
			}
		}
	}
}
