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
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/kexec"
	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/network/frr"
	"github.com/telekom/BOOTy/pkg/network/gobgp"
	"github.com/telekom/BOOTy/pkg/network/vlan"
	"github.com/telekom/BOOTy/pkg/plunderclient"
	"github.com/telekom/BOOTy/pkg/plunderclient/types"
	"github.com/telekom/BOOTy/pkg/provision"
	"github.com/telekom/BOOTy/pkg/utils"
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

	// Detect mode: CAPRF (ISO with /deploy/vars) or legacy (BOOTYURL env).
	if _, err := os.Stat(varsPath); err == nil {
		runCAPRF(ctx)
	} else {
		// Legacy mode: use proper DHCP mode to get network connectivity,
		// then proceed with plunder client provisioning.
		slog.Info("Legacy mode — starting DHCP for network connectivity")
		dhcp := network.NewDHCPMode()
		if err := dhcp.Setup(ctx, nil); err != nil {
			slog.Error("DHCP setup failed", "error", err)
			realm.Reboot()
		}
		runLegacy()
	}
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

// loadModules loads kernel modules from /modules/ for common server NICs.
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
		if err := orch.Provision(ctx); err != nil {
			slog.Error("Provisioning failed", "error", err)
		}
	}

	slog.Info("BOOTy CAPRF complete")
	if err := netMode.Teardown(ctx); err != nil {
		slog.Warn("Network teardown error", "error", err)
	}

	// Attempt kexec into installed kernel; fall back to normal reboot.
	if cfg.Mode != "deprovision" && cfg.Mode != "soft" {
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
			name, err := vlan.Setup(vlan.Config{
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
					if err := orch.Provision(ctx); err != nil {
						slog.Error("Hot provision failed", "error", err)
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

// runLegacy runs the original BOOTy flow using BOOTYURL environment variable.
func runLegacy() {
	mac, err := realm.GetMAC()
	if err != nil {
		slog.Error("Failed to get MAC address", "error", err)
		realm.Shell()
	}

	cfg, err := plunderclient.GetConfigForAddress(utils.DashMac(mac))
	if err != nil {
		slog.Error("Error with remote server", "error", err)
		slog.Info("Rebooting in 10 seconds")
		time.Sleep(time.Second * 10)
		realm.Reboot()
	}

	switch cfg.Action {
	case types.ReadImage:
		err = image.Read(cfg.SourceDevice, cfg.DestinationAddress, mac, cfg.Compressed)
		if err != nil {
			slog.Error("Read Image Error", "error", err)
			onError(cfg)
		}
		slog.Info("Image written successfully, restarting in 5 seconds")
		time.Sleep(time.Second * 5)
		realm.Reboot()

	case types.WriteImage:
		err = image.Write(cfg.SourceImage, cfg.DestinationDevice, cfg.Compressed)
		if err != nil {
			slog.Error("Write Image Error", "error", err)
			onError(cfg)
		}

	default:
		slog.Error("Unknown action passed to deployment image, restarting in 10 seconds", "action", cfg.Action)
		time.Sleep(time.Second * 10)
		realm.Reboot()
	}

	slog.Info("Beginning Disk Management")

	err = realm.PartProbe(cfg.DestinationDevice)
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	err = realm.EnableLVM()
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	rv, err := realm.MountRootVolume(cfg.LVMRootName)
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	err = realm.GrowLVMRoot(cfg.DestinationDevice, cfg.LVMRootName, cfg.GrowPartition)
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	slog.Info("Starting Networking configuration")
	err = realm.WriteNetPlan("/mnt", cfg)
	if err != nil {
		slog.Error("Network Error", "error", err)
		onError(cfg)
	}

	slog.Info("Applying Networking configuration")
	err = realm.ApplyNetplan("/mnt")
	if err != nil {
		slog.Error("Network Error", "error", err)
		onError(cfg)
	}

	slog.Info("Un Mounting boot volume")
	err = rv.UnMountNamed("dev")
	if err != nil {
		slog.Error("UnMounting Error", "error", err)
		onError(cfg)
	}
	err = rv.UnMountNamed("proc")
	if err != nil {
		slog.Error("UnMounting Error", "error", err)
		onError(cfg)
	}
	err = rv.UnMountAll()
	if err != nil {
		slog.Error("UnMounting Error", "error", err)
		onError(cfg)
	}

	if cfg.DropToShell {
		realm.Shell()
	}

	slog.Info("BOOTy is now exiting, system will reboot")
	time.Sleep(time.Second * 2)
	realm.Reboot()
}

// onError handles error recovery in legacy mode.
func onError(cfg *types.BootyConfig) {
	if cfg.WipeDevice {
		if err := realm.Wipe(cfg.DestinationDevice); err != nil {
			slog.Error("Wipe error", "error", err)
		}
	}

	if cfg.DropToShell {
		realm.Shell()
	}
	time.Sleep(time.Second * 2)
	realm.Reboot()
}
