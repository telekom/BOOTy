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

	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/crash"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/kexec"
	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/network/frr"
	"github.com/telekom/BOOTy/pkg/network/gobgp"
	"github.com/telekom/BOOTy/pkg/network/netplan"
	"github.com/telekom/BOOTy/pkg/network/vlan"
	"github.com/telekom/BOOTy/pkg/provision"
	"github.com/telekom/BOOTy/pkg/realm"
	"github.com/telekom/BOOTy/pkg/runmode"
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

	slog.Info("starting BOOTy", "version", Version, "build", Build)
	ux.Captain()
	ux.SysInfo()

	slog.Info("beginning provisioning process")
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
		slog.Error("failed to create folders", "error", err)
	}
	if err := m.MountNamed("dev", true); err != nil {
		slog.Error("failed to mount dev", "error", err)
	}
	if err := d.CreateDevice(); err != nil {
		slog.Error("failed to create devices", "error", err)
	}
	if err := m.MountAll(); err != nil {
		slog.Error("failed to mount filesystems", "error", err)
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
		slog.Debug("no kernel modules directory, skipping", "path", moduleDir)
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
					slog.Debug("module load skipped", "module", name, "error", err)
				}
				continue
			}
			slog.Info("loaded kernel module", "module", name)
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
		slog.Error("failed to create CAPRF client", "error", err)
		provision.DumpDebugState("caprf-init")
		realm.Reboot()
	}

	cfg, err := client.GetConfig(ctx)
	if err != nil {
		slog.Error("failed to get CAPRF config", "error", err)
		provision.DumpDebugState("config-fetch")
		realm.Reboot()
	}

	// DRY_RUN=true overrides mode before logging.
	if cfg.DryRun {
		cfg.Mode = "dry-run"
	}

	// Wire remote log shipping.
	if cfg.Transport.LogURL != "" {
		remote := caprf.NewRemoteHandler(client, slog.Default().Handler(), slog.LevelInfo, 256)
		defer remote.Close()
		slog.SetDefault(slog.New(remote))
	}

	slog.Info("CAPRF mode active",
		"hostname", cfg.Hostname,
		"mode", cfg.Mode,
		"image_count", len(cfg.Provision.Image.URLs),
	)

	netMode, err := setupNetworkAndTokenFlow(ctx, cfg, client)
	if err != nil {
		realm.Reboot()
	}

	diskMgr := disk.NewManager(nil)
	if result, inspectErr := crash.InspectStartup(ctx, cfg, diskMgr, client, crash.InspectOptions{}); inspectErr != nil {
		slog.Warn("startup crash artifact inspection failed", "error", inspectErr)
	} else if result != nil && result.Ran {
		slog.Info("startup crash artifact inspection complete",
			"evidence", result.EvidenceFound,
			"uploaded", result.Uploaded,
			"skip_reason", result.SkipReason)
	}

	// Resolve and run the operating mode.
	mode, err := runmode.Resolve(runmode.Deps{
		Cfg:     cfg,
		Client:  client,
		DiskMgr: diskMgr,
		NetMode: netMode,
	})
	if err != nil {
		slog.Error("unknown operating mode", "mode", cfg.Mode, "error", err)
		if netMode != nil {
			if tearErr := netMode.Teardown(ctx); tearErr != nil {
				slog.Warn("network teardown error", "error", tearErr)
			}
		}
		realm.Reboot()
		return
	}

	slog.Info("executing mode", "mode", mode.Name())
	modeErr := mode.Run(ctx)

	slog.Info("CAPRF run complete")

	// Handle mode-specific exit behavior before network teardown,
	// so rescue shell SSH access remains available.
	var rescueErr *runmode.RescueShellError
	var rebootErr *runmode.RebootRequestedError
	var provisionErr *runmode.ProvisionCompleteError
	switch {
	case errors.As(modeErr, &rescueErr):
		// Network teardown is intentionally skipped here so SSH access
		// remains available in the rescue shell.
		realm.Shell()
		realm.Reboot()
		return
	case errors.As(modeErr, &rebootErr):
		if netMode != nil {
			if err := netMode.Teardown(ctx); err != nil {
				slog.Warn("network teardown error", "error", err)
			}
		}
		realm.Reboot()
		return
	case errors.As(modeErr, &provisionErr):
		if netMode != nil {
			if err := netMode.Teardown(ctx); err != nil {
				slog.Warn("network teardown error", "error", err)
			}
		}
		tryKexec(cfg, provisionErr.FirmwareChanged)
		time.Sleep(2 * time.Second)
		if provisionErr.PowerOff {
			slog.Info("provisioning succeeded, powering off for orchestrator to manage boot")
			realm.PowerOff()
		} else {
			realm.Reboot()
		}
		return
	}

	if modeErr != nil {
		slog.Error("mode exited with error", "mode", mode.Name(), "error", modeErr)
	}
	if netMode != nil {
		if err := netMode.Teardown(ctx); err != nil {
			slog.Warn("network teardown error", "error", err)
		}
	}
	time.Sleep(2 * time.Second)
	realm.Reboot()
}

func setupNetworkAndTokenFlow(ctx context.Context, cfg *config.MachineConfig, client *caprf.Client) (network.Mode, error) {
	if cfg.Mode == "dry-run" {
		slog.Info("dry-run mode: skipping active network setup and token renewal")
		return noopNetworkMode{}, nil
	}

	// Set up networking with retry — if connectivity fails, teardown and
	// rebuild the entire network stack before giving up.
	netMode, err := setupNetworkMode(ctx, cfg)
	if err != nil {
		return nil, err
	}
	connectivityTarget := cfg.Transport.InitURL
	if connectivityTarget == "" {
		connectivityTarget = cfg.Transport.SuccessURL
	}
	if connectivityTarget == "" && cfg.Provision.CrashArtifacts.Enabled {
		connectivityTarget = cfg.Provision.CrashArtifacts.PrepareURL
		if connectivityTarget == "" {
			connectivityTarget = cfg.Provision.CrashArtifacts.UploadURL
		}
	}
	if connectivityTarget != "" {
		activeMode, err := ensureNetworkConnectivity(ctx, cfg, netMode, connectivityTarget)
		if err != nil {
			return nil, err
		}
		netMode = activeMode
	}

	// Acquire JWT after network is ready so the token endpoint is reachable.
	if err := client.AcquireToken(ctx); err != nil {
		slog.Error("failed to acquire jwt token", "error", err)
		if cfg.Transport.TokenURL != "" {
			provision.DumpDebugState("jwt-acquire")
			return nil, err
		}
	}

	client.SetTokenRenewalFatalHandler(func() {
		slog.Error("token renewal exhausted, rebooting")
		provision.DumpDebugState("jwt-renewal-exhausted")
		realm.Reboot()
	})

	if err := client.StartTokenRenewal(ctx); err != nil {
		slog.Error("failed to start token renewal", "error", err)
		if cfg.Transport.TokenURL != "" {
			provision.DumpDebugState("jwt-renewal-start")
			return nil, err
		}
	}

	return netMode, nil
}

type noopNetworkMode struct{}

func (noopNetworkMode) Setup(context.Context, *network.Config) error { return nil }

func (noopNetworkMode) WaitForConnectivity(context.Context, string, time.Duration) error { return nil }

func (noopNetworkMode) Teardown(context.Context) error { return nil }

// ensureNetworkConnectivity retries network setup up to 3 times on connectivity failure.
// It returns the active network mode so callers can tear down the latest instance.
func ensureNetworkConnectivity(ctx context.Context, cfg *config.MachineConfig, netMode network.Mode, target string) (network.Mode, error) {
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		slog.Info("waiting for network connectivity", "target", target, "attempt", attempt)
		if err := netMode.WaitForConnectivity(ctx, target, 5*time.Minute); err == nil {
			slog.Info("network connectivity established", "target", target)
			return netMode, nil
		}
		slog.Error("network connectivity timeout", "attempt", attempt)
		if attempt < maxRetries {
			slog.Info("tearing down network for retry", "attempt", attempt)
			if tErr := netMode.Teardown(ctx); tErr != nil {
				slog.Warn("network teardown failed", "error", tErr)
			}
			newMode, setupErr := setupNetworkMode(ctx, cfg)
			if setupErr != nil {
				return nil, fmt.Errorf("network retry setup: %w", setupErr)
			}
			netMode = newMode
		}
	}
	slog.Error("network connectivity failed after all retries", "attempts", maxRetries)
	return netMode, fmt.Errorf("network connectivity timeout after %d attempts", maxRetries)
}

// setupNetworkMode detects and configures the appropriate network mode.
func setupNetworkMode(ctx context.Context, cfg *config.MachineConfig) (network.Mode, error) {
	netCfg := &network.Config{
		UnderlaySubnet:   cfg.Network.EVPN.UnderlaySubnet,
		UnderlayIP:       cfg.Network.EVPN.UnderlayIP,
		OverlaySubnet:    cfg.Network.EVPN.OverlaySubnet,
		IPMISubnet:       cfg.Network.IPMI.Subnet,
		ASN:              cfg.Network.BGP.ASN,
		ProvisionVNI:     cfg.Network.EVPN.ProvisionVNI,
		ProvisionIP:      cfg.Network.EVPN.ProvisionIP,
		ProvisionGateway: cfg.Network.EVPN.ProvisionGateway,
		DNSResolvers:     cfg.Network.DNSResolvers,
		DCGWIPs:          cfg.Network.EVPN.DCGWIPs,
		LeafASN:          cfg.Network.EVPN.LeafASN,
		LocalASN:         cfg.Network.EVPN.LocalASN,
		OverlayAggregate: cfg.Network.EVPN.OverlayAggregate,
		VPNRT:            cfg.Network.EVPN.VPNRT,
		StaticIP:         cfg.Network.Static.IP,
		StaticGateway:    cfg.Network.Static.Gateway,
		StaticIface:      cfg.Network.Static.Iface,
		BondInterfaces:   cfg.Network.Bond.Interfaces,
		BondMode:         cfg.Network.Bond.Mode,
		VRFName:          cfg.Network.VRF.Name,
		VRFTableID:       cfg.Network.VRF.TableID,
		BGPKeepalive:     cfg.Network.BGP.Keepalive,
		BGPHold:          cfg.Network.BGP.Hold,
		BFDTransmitMS:    cfg.Network.BGP.BFDTransmitMS,
		BFDReceiveMS:     cfg.Network.BGP.BFDReceiveMS,
		NetworkMode:      cfg.Network.Mode,
		BGPPeerMode:      network.ParsePeerMode(cfg.Network.BGP.PeerMode),
		BGPNeighbors:     cfg.Network.BGP.Neighbors,
		BGPRemoteASN:     cfg.Network.BGP.RemoteASN,
		BGPUnderlayAF:    cfg.Network.BGP.UnderlayAF,
		BGPOverlayType:   cfg.Network.BGP.OverlayType,
		EVPNL2Enabled:    cfg.Network.EVPN.L2Enabled,
		BGPAuthPassword:  cfg.Network.BGP.AuthPassword,
		BGPMinPeers:      cfg.Network.BGP.MinPeers,
	}

	// Auto-detect netplan configuration files injected by the provisioner.
	// Netplan-derived values override vars-based values, allowing the same
	// config format used by the old deployer to work as a drop-in with BOOTy.
	if npCfg := detectNetplanConfig(); npCfg != nil {
		mergeNetplanConfig(netCfg, npCfg)
	}

	// Parse VLAN configuration.
	if cfg.Network.VLAN.Config != "" {
		vlans, err := network.ParseVLANs(cfg.Network.VLAN.Config)
		if err != nil {
			return nil, fmt.Errorf("invalid VLAN configuration: %w", err)
		}
		netCfg.VLANs = vlans
	}

	// Set up VLANs first — they create sub-interfaces that other modes use.
	if netCfg.IsVLANMode() {
		slog.Info("setting up VLAN interfaces", "count", len(netCfg.VLANs))
		var vlanErrs []error
		for _, v := range netCfg.VLANs {
			name, err := vlan.Setup(&vlan.Config{
				ID:      v.ID,
				Parent:  v.Parent,
				Address: v.Address,
				Gateway: v.Gateway,
			})
			if err != nil {
				slog.Error("VLAN setup failed", "vlan", v.ID, "parent", v.Parent, "error", err)
				vlanErrs = append(vlanErrs, fmt.Errorf("vlan %d on %s: %w", v.ID, v.Parent, err))
				continue
			}
			if netCfg.StaticIface == "" {
				netCfg.StaticIface = name
			}
		}
		if err := errors.Join(vlanErrs...); err != nil {
			return nil, fmt.Errorf("vlan setup: %w", err)
		}
	}

	// Set up bonding if configured (bond becomes the interface for other modes).
	if netCfg.IsBondMode() {
		slog.Info("setting up LACP bond")
		bond := &network.BondMode{}
		if err := bond.Setup(ctx, netCfg); err != nil {
			return nil, fmt.Errorf("bond setup: %w", err)
		}
		if netCfg.StaticIface == "" {
			netCfg.StaticIface = "bond0"
		}
	}

	// Priority: GoBGP > FRR > Static > DHCP.
	if netCfg.IsGoBGPMode() {
		detectIPMI(netCfg)
		slog.Info("using GoBGP/EVPN network mode", "asn", cfg.Network.BGP.ASN)
		stack, err := setupGoBGPStack(ctx, netCfg)
		if err != nil {
			slog.Error("goBGP setup failed, falling back to FRR", "error", err)
			mgr := frr.NewManager(nil)
			if frrErr := mgr.Setup(ctx, netCfg); frrErr != nil {
				slog.Error("FRR fallback also failed", "error", frrErr)
				mgr.DumpFRRState()
				return dhcpFallback(ctx, netCfg), nil
			}
			return mgr, nil
		}
		return stack, nil
	}

	if netCfg.IsFRRMode() {
		detectIPMI(netCfg)
		slog.Info("using FRR/EVPN network mode", "asn", cfg.Network.BGP.ASN)
		mgr := frr.NewManager(nil)
		if err := mgr.Setup(ctx, netCfg); err != nil {
			slog.Error("FRR network setup failed, falling back to DHCP", "error", err)
			mgr.DumpFRRState()
			return dhcpFallback(ctx, netCfg), nil
		}
		return mgr, nil
	}

	if netCfg.IsStaticMode() {
		slog.Info("using static network mode", "ip", cfg.Network.Static.IP)
		mode := &network.StaticMode{}
		if err := mode.Setup(ctx, netCfg); err != nil {
			slog.Error("static network setup failed, falling back to DHCP", "error", err)
			return dhcpFallback(ctx, netCfg), nil
		}
		return mode, nil
	}

	slog.Info("using DHCP network mode")
	return dhcpFallback(ctx, netCfg), nil
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
		slog.Warn("failed to detect IPMI info", "error", err)
		return
	}
	if netCfg.IPMIMAC == "" {
		netCfg.IPMIMAC = mac
	}
	if netCfg.IPMIIP == "" {
		netCfg.IPMIIP = ip
	}
	slog.Info("detected IPMI info", "mac", mac, "ip", ip)
}

// tryKexec attempts to kexec into the installed kernel.
// Falls back silently on failure so the caller can do a normal reboot.
// Skips kexec when disabled by config toggle or when firmware was changed during
// provisioning (e.g. Mellanox SR-IOV), since firmware reinit requires a hard reboot.
func tryKexec(cfg *config.MachineConfig, firmwareChanged bool) {
	if cfg.Provision.DisableKexec {
		slog.Info("kexec disabled by configuration, skipping")
		return
	}

	if firmwareChanged {
		slog.Info("firmware values changed during provisioning, hard reboot required — skipping kexec")
		return
	}

	const grubPath = "/newroot/boot/grub/grub.cfg"
	f, err := os.Open(grubPath)
	if err != nil {
		slog.Warn("cannot open grub.cfg, skipping kexec", "error", err)
		return
	}
	defer func() { _ = f.Close() }()

	entries, err := kexec.ParseGrubCfg(f)
	if err != nil {
		slog.Warn("failed to parse grub.cfg", "error", err)
		return
	}
	entry, err := kexec.GetDefaultEntry(entries)
	if err != nil {
		slog.Warn("no default boot entry found", "error", err)
		return
	}

	kernel := "/newroot" + entry.Kernel
	initrd := "/newroot" + entry.Initramfs
	slog.Info("attempting kexec", "kernel", kernel, "initrd", initrd)

	if err := kexec.Load(kernel, initrd, entry.KernelArgs); err != nil {
		slog.Warn("kexec load failed, falling back to reboot", "error", err)
		return
	}
	if err := kexec.Execute(); err != nil {
		slog.Warn("kexec execute failed, falling back to reboot", "error", err)
	}
}
