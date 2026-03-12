//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/kexec"
	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/network/frr"
	"github.com/telekom/BOOTy/pkg/plunderclient"
	"github.com/telekom/BOOTy/pkg/plunderclient/types"
	"github.com/telekom/BOOTy/pkg/provision"
	"github.com/telekom/BOOTy/pkg/utils"
	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/realm"
	"github.com/telekom/BOOTy/pkg/ux"
)

const varsPath = "/deploy/vars"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	setupMountsAndDevices()
	loadModules()

	slog.Info("Starting DHCP client")
	go func() {
		if err := realm.DHCPClient(); err != nil {
			slog.Error("DHCP client error", "error", err)
		}
	}()

	slog.Info("Starting BOOTy")
	time.Sleep(time.Second * 2)
	ux.Captain()
	ux.SysInfo()

	slog.Info("Beginning provisioning process")
	ctx := context.Background()

	// Detect mode: CAPRF (ISO with /deploy/vars) or legacy (BOOTYURL env).
	if _, err := os.Stat(varsPath); err == nil {
		runCAPRF(ctx)
	} else {
		runLegacy()
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
func loadModules() {
	const moduleDir = "/modules"
	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		slog.Debug("No kernel modules directory, skipping", "path", moduleDir)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ko := filepath.Join(moduleDir, entry.Name())
		if err := loadModule(ko); err != nil {
			slog.Debug("Module load skipped", "module", entry.Name(), "error", err)
			continue
		}
		slog.Info("Loaded kernel module", "module", entry.Name())
	}
}

// loadModule loads a single kernel module via the finit_module syscall.
func loadModule(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open module %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := unix.FinitModule(int(f.Fd()), "", 0); err != nil {
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
		realm.Reboot()
	}

	cfg, err := client.GetConfig(ctx)
	if err != nil {
		slog.Error("Failed to get CAPRF config", "error", err)
		realm.Reboot()
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
		"images", cfg.ImageURLs,
	)

	// Set up networking based on configuration.
	netMode := setupNetworkMode(ctx, cfg)

	// Wait for network connectivity before proceeding.
	connectivityTarget := cfg.InitURL
	if connectivityTarget == "" {
		connectivityTarget = cfg.SuccessURL
	}
	if connectivityTarget != "" {
		slog.Info("Waiting for network connectivity", "target", connectivityTarget)
		if err := netMode.WaitForConnectivity(ctx, connectivityTarget, 5*time.Minute); err != nil {
			slog.Error("Network connectivity timeout", "error", err)
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
	}

	// Set up bonding first if configured (bond becomes the interface for other modes).
	if netCfg.IsBondMode() {
		slog.Info("Setting up LACP bond")
		bond := &network.BondMode{}
		if err := bond.Setup(ctx, netCfg); err != nil {
			slog.Error("Bond setup failed", "error", err)
		} else {
			// The bond interface becomes the static/DHCP interface.
			if netCfg.StaticIface == "" {
				netCfg.StaticIface = "bond0"
			}
		}
	}

	// Priority: FRR > Static > DHCP.
	if netCfg.IsFRRMode() {
		detectIPMI(netCfg)
		slog.Info("Using FRR/EVPN network mode", "asn", cfg.ASN)
		mgr := frr.NewManager(nil)
		if err := mgr.Setup(ctx, netCfg); err != nil {
			slog.Error("FRR network setup failed, falling back to DHCP", "error", err)
			return &network.DHCPMode{}
		}
		return mgr
	}

	if netCfg.IsStaticMode() {
		slog.Info("Using static network mode", "ip", cfg.StaticIP)
		mode := &network.StaticMode{}
		if err := mode.Setup(ctx, netCfg); err != nil {
			slog.Error("Static network setup failed, falling back to DHCP", "error", err)
			return &network.DHCPMode{}
		}
		return mode
	}

	slog.Info("Using DHCP network mode")
	return &network.DHCPMode{}
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
					}
					if err := netMode.Teardown(ctx); err != nil {
						slog.Warn("Network teardown error", "error", err)
					}
					time.Sleep(2 * time.Second)
					realm.Reboot()
					return
				case "reboot":
					slog.Info("Reboot command received")
					if err := netMode.Teardown(ctx); err != nil {
						slog.Warn("Network teardown error", "error", err)
					}
					realm.Reboot()
					return
				default:
					slog.Warn("Unknown command type", "type", cmd.Type)
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
