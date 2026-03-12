//go:build linux

package frr

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/telekom/BOOTy/pkg/network"
)

// Manager handles FRR/EVPN network setup and teardown.
type Manager struct {
	cfg       network.Config
	commander Commander
}

// Commander abstracts command execution for testing.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecCommander executes real system commands.
type ExecCommander struct{}

// Run executes a command and returns combined output.
func (e *ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("exec %s: %w", name, err)
	}
	return out, nil
}

// NewManager creates an FRR manager.
func NewManager(commander Commander) *Manager {
	if commander == nil {
		commander = &ExecCommander{}
	}
	return &Manager{commander: commander}
}

// Setup configures the full FRR/EVPN network stack.
func (m *Manager) Setup(ctx context.Context, cfg *network.Config) error {
	cfg.ApplyDefaults()
	m.cfg = *cfg

	underlayIP, overlayIP, bridgeMAC, err := DeriveAddresses(cfg)
	if err != nil {
		return fmt.Errorf("derive addresses: %w", err)
	}

	slog.Info("FRR setup",
		"underlay_ip", underlayIP,
		"overlay_ip", overlayIP,
		"bridge_mac", bridgeMAC,
		"asn", cfg.ASN,
		"vni", cfg.ProvisionVNI,
	)

	nics, err := m.setupInterfaces(cfg, underlayIP, overlayIP, bridgeMAC)
	if err != nil {
		return err
	}

	if err := m.startFRRStack(ctx, cfg, underlayIP, overlayIP, nics); err != nil {
		return err
	}

	slog.Info("FRR/EVPN network setup complete", "nics", nics)
	return nil
}

// setupInterfaces creates VRF, dummy, VXLAN, bridge, loopback, and configures NICs.
func (m *Manager) setupInterfaces(cfg *network.Config, underlayIP, overlayIP, bridgeMAC string) ([]string, error) {
	if cfg.VRFName != "" {
		if err := m.createVRF(cfg.VRFName, cfg.VRFTableID); err != nil {
			return nil, fmt.Errorf("create VRF: %w", err)
		}
	}

	if err := m.createDummy("dummy.underlay", cfg.VRFName, underlayIP+"/32"); err != nil {
		slog.Warn("Cannot create dummy interface, using loopback for underlay IP", "error", err)
		if loErr := m.addLoopbackAddress(underlayIP); loErr != nil {
			return nil, fmt.Errorf("add underlay IP to loopback: %w", loErr)
		}
	}

	if err := m.createVXLAN(cfg.ProvisionVNI, underlayIP, cfg.BridgeName, bridgeMAC, cfg.MTU); err != nil {
		return nil, fmt.Errorf("create VXLAN: %w", err)
	}

	if cfg.ProvisionIP != "" {
		if err := m.addBridgeAddress(cfg.BridgeName, cfg.ProvisionIP); err != nil {
			return nil, fmt.Errorf("add bridge address: %w", err)
		}
	}

	if err := m.addLoopbackAddress(overlayIP); err != nil {
		return nil, fmt.Errorf("add loopback address: %w", err)
	}

	nics, err := network.DetectPhysicalNICs()
	if err != nil {
		return nil, fmt.Errorf("detect NICs: %w", err)
	}

	if err := m.configureNICs(nics, cfg.VRFName, cfg.MTU); err != nil {
		return nil, fmt.Errorf("configure NICs: %w", err)
	}

	if err := m.enableForwarding(); err != nil {
		slog.Warn("Failed to enable IP forwarding", "error", err)
	}

	return nics, nil
}

// startFRRStack renders config, writes it, starts FRR daemons, and adds BGP peers.
func (m *Manager) startFRRStack(ctx context.Context, cfg *network.Config, underlayIP, overlayIP string, nics []string) error {
	frrConf, err := RenderConfig(cfg, underlayIP, overlayIP, nics)
	if err != nil {
		return fmt.Errorf("render FRR config: %w", err)
	}

	if err := m.writeFRRConfig(frrConf); err != nil {
		return fmt.Errorf("write FRR config: %w", err)
	}

	if err := m.writeDaemonsConfig(); err != nil {
		return fmt.Errorf("write daemons config: %w", err)
	}

	ensureFRRDirs()

	if err := m.startFRR(ctx); err != nil {
		return fmt.Errorf("start FRR: %w", err)
	}

	for _, nic := range nics {
		if err := m.addBGPPeer(ctx, cfg.VRFName, cfg.ASN, nic); err != nil {
			slog.Warn("Failed to add BGP peer", "nic", nic, "error", err)
		}
	}

	return nil
}

// WaitForConnectivity polls the target URL until reachable, restarting FRR periodically.
func (m *Manager) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return waitForHTTPWithFRR(ctx, target, timeout, m)
}

// Teardown removes the FRR network configuration.
func (m *Manager) Teardown(ctx context.Context) error {
	_, _ = m.commander.Run(ctx, "systemctl", "stop", "frr")
	slog.Info("FRR teardown complete")
	return nil
}

// DumpFRRState logs FRR diagnostic state via the commander abstraction.
// Called on FRR setup failure or connectivity timeout to capture BGP/EVPN state.
func (m *Manager) DumpFRRState() {
	ctx := context.Background()
	type frrCmd struct {
		label string
		args  []string
	}
	cmds := []frrCmd{
		{"bgp summary", []string{"-c", "show bgp summary"}},
		{"bgp ipv4", []string{"-c", "show bgp ipv4 unicast"}},
		{"bgp ipv6", []string{"-c", "show bgp ipv6 unicast"}},
		{"bgp l2vpn evpn", []string{"-c", "show bgp l2vpn evpn"}},
		{"bfd peers", []string{"-c", "show bfd peers"}},
		{"interface brief", []string{"-c", "show interface brief"}},
	}
	slog.Error("=== FRR STATE DUMP START ===")
	for _, c := range cmds {
		out, err := m.commander.Run(ctx, "vtysh", c.args...)
		if err != nil {
			slog.Error("FRR dump command failed", "label", c.label, "error", err)
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				slog.Error("FRR", "label", c.label, "data", line)
			}
		}
	}
	slog.Error("=== FRR STATE DUMP END ===")
}

// ensureFRRDirs creates runtime directories that FRR daemons expect.
// Without these, zebra cannot create its zserv.api socket and bgpd
// cannot communicate with zebra, breaking BGP unnumbered.
func ensureFRRDirs() {
	for _, d := range []string{"/var/run/frr", "/var/tmp/frr", "/var/lib/frr"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			slog.Warn("Failed to create FRR directory", "path", d, "error", err)
		}
	}
}

func (m *Manager) createVRF(name string, tableID uint32) error {
	vrf := &netlink.Vrf{
		LinkAttrs: netlink.LinkAttrs{Name: name},
		Table:     tableID,
	}
	if err := netlink.LinkAdd(vrf); err != nil {
		if os.IsExist(err) {
			slog.Debug("VRF already exists", "name", name)
			return nil
		}
		return fmt.Errorf("add VRF %s: %w", name, err)
	}
	if err := netlink.LinkSetUp(vrf); err != nil {
		return fmt.Errorf("bring up VRF %s: %w", name, err)
	}
	return nil
}

func (m *Manager) createDummy(name, vrfName, addr string) error {
	dummy := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: name},
	}
	if err := netlink.LinkAdd(dummy); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add dummy %s: %w", name, err)
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("find dummy %s: %w", name, err)
	}

	if vrfName != "" {
		vrfLink, err := netlink.LinkByName(vrfName)
		if err != nil {
			return fmt.Errorf("find VRF %s: %w", vrfName, err)
		}
		if err := netlink.LinkSetMasterByIndex(link, vrfLink.Attrs().Index); err != nil {
			return fmt.Errorf("assign dummy to VRF: %w", err)
		}
	}

	nlAddr, err := netlink.ParseAddr(addr)
	if err != nil {
		return fmt.Errorf("parse addr %s: %w", addr, err)
	}
	if err := netlink.AddrAdd(link, nlAddr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add addr to dummy: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up dummy %s: %w", name, err)
	}
	return nil
}

func (m *Manager) createVXLAN(vni uint32, srcIP, bridgeName, bridgeMAC string, physicalMTU int) error {
	vxlanName := fmt.Sprintf("vx%d", vni)

	// VXLAN MTU = physical MTU minus 50 bytes overhead (outer IP + UDP + VXLAN headers).
	vxlanMTU := physicalMTU - 50
	if vxlanMTU <= 0 {
		vxlanMTU = 1500
	}

	srcAddr := net.ParseIP(srcIP)
	vxlan := &netlink.Vxlan{
		LinkAttrs:    netlink.LinkAttrs{Name: vxlanName},
		VxlanId:      int(vni),
		SrcAddr:      srcAddr,
		Port:         4789,
		Learning:     false,
		VtepDevIndex: 0,
	}

	if err := netlink.LinkAdd(vxlan); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add VXLAN %s: %w", vxlanName, err)
	}

	vxLink, err := netlink.LinkByName(vxlanName)
	if err != nil {
		return fmt.Errorf("find VXLAN: %w", err)
	}
	if err := netlink.LinkSetMTU(vxLink, vxlanMTU); err != nil {
		return fmt.Errorf("set VXLAN MTU: %w", err)
	}

	hwAddr, err := net.ParseMAC(bridgeMAC)
	if err != nil {
		return fmt.Errorf("parse bridge MAC %s: %w", bridgeMAC, err)
	}

	bridge := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name:         bridgeName,
			HardwareAddr: hwAddr,
		},
	}
	if err := netlink.LinkAdd(bridge); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add bridge %s: %w", bridgeName, err)
	}

	brLink, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return fmt.Errorf("find bridge: %w", err)
	}

	if err := netlink.LinkSetMasterByIndex(vxLink, brLink.Attrs().Index); err != nil {
		return fmt.Errorf("attach VXLAN to bridge: %w", err)
	}

	if err := netlink.LinkSetUp(brLink); err != nil {
		return fmt.Errorf("bring up bridge: %w", err)
	}
	if err := netlink.LinkSetUp(vxLink); err != nil {
		return fmt.Errorf("bring up VXLAN: %w", err)
	}
	return nil
}

func (m *Manager) addLoopbackAddress(ip string) error {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("find loopback: %w", err)
	}

	addr, err := netlink.ParseAddr(ip + "/128")
	if err != nil {
		addr, err = netlink.ParseAddr(ip + "/32")
		if err != nil {
			return fmt.Errorf("parse overlay IP %s: %w", ip, err)
		}
	}

	if err := netlink.AddrAdd(lo, addr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add overlay IP to loopback: %w", err)
	}

	return nil
}

func (m *Manager) addBridgeAddress(bridgeName, addr string) error {
	link, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return fmt.Errorf("find bridge %s: %w", bridgeName, err)
	}

	nlAddr, err := netlink.ParseAddr(addr)
	if err != nil {
		return fmt.Errorf("parse provision IP %s: %w", addr, err)
	}

	if err := netlink.AddrAdd(link, nlAddr); err != nil && !os.IsExist(err) {
		return fmt.Errorf("add provision IP to bridge: %w", err)
	}

	slog.Info("Assigned provision IP to bridge", "bridge", bridgeName, "ip", addr)
	return nil
}

func (m *Manager) configureNICs(nics []string, vrfName string, mtu int) error {
	for _, nic := range nics {
		if err := m.configureNIC(nic, vrfName, mtu); err != nil {
			slog.Warn("Failed to configure NIC", "nic", nic, "error", err)
		}
	}
	return nil
}

func (m *Manager) configureNIC(name, vrfName string, mtu int) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("find NIC %s: %w", name, err)
	}

	if err := netlink.LinkSetMTU(link, mtu); err != nil {
		return fmt.Errorf("set MTU on %s: %w", name, err)
	}

	if vrfName != "" {
		vrfLink, err := netlink.LinkByName(vrfName)
		if err != nil {
			return fmt.Errorf("find VRF %s: %w", vrfName, err)
		}
		if err := netlink.LinkSetMasterByIndex(link, vrfLink.Attrs().Index); err != nil {
			return fmt.Errorf("assign %s to VRF: %w", name, err)
		}
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up NIC %s: %w", name, err)
	}
	return nil
}

func (m *Manager) enableForwarding() error {
	sysctls := map[string]string{
		"/proc/sys/net/ipv4/ip_forward":                    "1",
		"/proc/sys/net/ipv6/conf/all/forwarding":           "2",
		"/proc/sys/net/ipv4/conf/all/rp_filter":            "0",
		"/proc/sys/net/ipv4/conf/default/rp_filter":        "0",
		"/proc/sys/net/ipv6/conf/all/accept_ra":            "2",
		"/proc/sys/net/ipv6/conf/all/accept_ra_defrtr":     "1",
		"/proc/sys/net/ipv6/conf/default/accept_ra":        "2",
		"/proc/sys/net/ipv6/conf/default/accept_ra_defrtr": "1",
	}

	for path, val := range sysctls {
		if err := os.WriteFile(path, []byte(val), 0o644); err != nil { //nolint:gosec // sysctl paths are trusted
			slog.Debug("Failed to set sysctl", "path", path, "error", err)
		}
	}
	return nil
}

func (m *Manager) writeFRRConfig(conf string) error {
	if err := os.MkdirAll("/etc/frr", 0o755); err != nil {
		return fmt.Errorf("create /etc/frr: %w", err)
	}
	if err := os.WriteFile("/etc/frr/frr.conf", []byte(conf), 0o644); err != nil {
		return fmt.Errorf("write frr.conf: %w", err)
	}
	// vtysh.conf must exist for integrated config mode.
	vtyshConf := "service integrated-vtysh-config\n"
	if err := os.WriteFile("/etc/frr/vtysh.conf", []byte(vtyshConf), 0o644); err != nil {
		return fmt.Errorf("write vtysh.conf: %w", err)
	}
	return nil
}

func (m *Manager) writeDaemonsConfig() error {
	const daemons = `# FRR daemons configuration - managed by BOOTy
zebra=yes
bgpd=yes
ospfd=no
ospf6d=no
ripd=no
ripngd=no
isisd=no
pimd=no
ldpd=no
nhrpd=no
eigrpd=no
babeld=no
sharpd=no
pbrd=no
bfdd=yes
fabricd=no
vrrpd=no
pathd=no

vtysh_enable=yes
zebra_options="  -A 127.0.0.1 -s 90000000"
bgpd_options="   -A 127.0.0.1"
bfdd_options="   -A 127.0.0.1"
`
	if err := os.WriteFile("/etc/frr/daemons", []byte(daemons), 0o640); err != nil {
		return fmt.Errorf("write daemons config: %w", err)
	}
	return nil
}

// startFRR launches FRR daemons using the best available method.
// All methods avoid CombinedOutput() because FRR daemons fork with -d,
// and child processes inherit pipes, blocking CombinedOutput() indefinitely.
func (m *Manager) startFRR(ctx context.Context) error {
	if err := runDaemonCmd(ctx, "systemctl", "restart", "frr"); err == nil {
		slog.Info("FRR daemons started via systemctl")
		return nil
	}

	initPath := "/usr/lib/frr/frrinit.sh"
	if _, statErr := os.Stat(initPath); statErr == nil {
		if err := runDaemonCmd(ctx, initPath, "start"); err == nil {
			slog.Info("FRR daemons started via frrinit.sh")
			return nil
		}
		slog.Warn("frrinit.sh start failed, falling back to direct daemon start")
	}

	return m.startDaemonsDirect(ctx)
}

// runDaemonCmd runs a command that may fork long-lived daemons.
// It uses os.Stderr directly (no pipes) so cmd.Wait returns when the
// parent process exits, not when all child pipe holders close.
func runDaemonCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", name, err)
	}

	return nil
}

func (m *Manager) startDaemonsDirect(ctx context.Context) error {
	// FRR 10.x daemon startup order: mgmtd → zebra → staticd → bgpd → bfdd.
	// When mgmtd is present (FRR 10.5+), it reads /etc/frr/frr.conf and
	// distributes config to other daemons — bgpd must NOT use -f in that case.
	// When mgmtd is absent (production initramfs), bgpd reads config via -f.
	type daemonSpec struct {
		name string
		args []string
	}

	hasMgmtd := false
	if _, err := os.Stat("/usr/lib/frr/mgmtd"); err == nil {
		hasMgmtd = true
	}

	bgpdArgs := []string{"-d", "-A", "127.0.0.1"}
	if !hasMgmtd {
		bgpdArgs = append(bgpdArgs, "-f", "/etc/frr/frr.conf")
	}

	daemons := []daemonSpec{
		{"mgmtd", []string{"-d", "-A", "127.0.0.1"}},
		{"zebra", []string{"-d", "-A", "127.0.0.1", "-s", "90000000"}},
		{"staticd", []string{"-d", "-A", "127.0.0.1"}},
		{"bgpd", bgpdArgs},
		{"bfdd", []string{"-d", "-A", "127.0.0.1"}},
	}
	for _, d := range daemons {
		path := "/usr/lib/frr/" + d.name
		if _, err := os.Stat(path); err != nil {
			slog.Debug("Daemon not found, skipping", "daemon", d.name)
			continue
		}
		if err := runDaemonCmd(ctx, path, d.args...); err != nil {
			slog.Warn("Failed to start daemon", "daemon", d.name, "error", err)
		} else {
			slog.Info("Started FRR daemon", "daemon", d.name)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (m *Manager) addBGPPeer(ctx context.Context, vrfName string, asn uint32, nic string) error {
	bgpCmd := fmt.Sprintf("router bgp %d", asn)
	if vrfName != "" {
		bgpCmd = fmt.Sprintf("router bgp %d vrf %s", asn, vrfName)
	}
	out, err := m.commander.Run(ctx, "vtysh",
		"-c", "conf t",
		"-c", bgpCmd,
		"-c", fmt.Sprintf("neighbor %s interface peer-group fabric", nic),
	)
	if err != nil {
		return fmt.Errorf("vtysh add peer %s: %w (output: %s)", nic, err, string(out))
	}
	slog.Info("Added BGP peer", "nic", nic, "vrf", vrfName)
	return nil
}

// waitForHTTPWithFRR polls target, restarting FRR every 20s if needed.
func waitForHTTPWithFRR(ctx context.Context, target string, timeout time.Duration, mgr *Manager) error {
	if target == "" {
		return fmt.Errorf("empty connectivity target URL")
	}

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}
	attempt := 0
	lastRestart := time.Now()
	const restartInterval = 120 * time.Second

	for time.Now().Before(deadline) {
		attempt++
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, http.NoBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req) //nolint:gosec // target URL from trusted config, not user input
		if err == nil {
			_ = resp.Body.Close()
			slog.Info("Network connectivity established",
				"target", target, "attempt", attempt)
			return nil
		}

		slog.Debug("Connectivity check failed",
			"target", target, "attempt", attempt, "error", err)

		if time.Since(lastRestart) >= restartInterval {
			slog.Info("Restarting FRR daemons for connectivity recovery")
			if sErr := runDaemonCmd(ctx, "systemctl", "restart", "frr"); sErr != nil {
				_ = runDaemonCmd(ctx, "/usr/lib/frr/frrinit.sh", "restart")
			}
			lastRestart = time.Now()
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("connectivity check canceled: %w", ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}

	if mgr != nil {
		slog.Error("Connectivity timeout — dumping FRR state for diagnostics")
		mgr.DumpFRRState()
	}
	return fmt.Errorf("network connectivity timeout after %s (%d attempts)", timeout, attempt)
}
