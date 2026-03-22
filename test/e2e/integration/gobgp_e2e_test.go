//go:build e2e_gobgp

// Package integration contains E2E tests for GoBGP PeerMode scenarios.
// These tests verify that BOOTy with GoBGP (no FRR) can establish BGP sessions
// in all three supported modes: unnumbered, dual, and numbered.
//
// Prerequisites:
//
//	make clab-gobgp-up   # deploys topology-gobgp.clab.yml
package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	gobgpLabSpine       = "clab-booty-gobgp-lab-spine01"
	gobgpLabRR          = "clab-booty-gobgp-lab-rr01"
	gobgpLabUnnumbered  = "clab-booty-gobgp-lab-booty-unnumbered"
	gobgpLabDual        = "clab-booty-gobgp-lab-booty-dual"
	gobgpLabNumbered    = "clab-booty-gobgp-lab-booty-numbered"
	gobgpLabClient      = "clab-booty-gobgp-lab-client"
	bgpConvergeTimeout  = 60 * time.Second
	bgpConvergeInterval = 2 * time.Second
)

// requireGoBGPLab fails the test if the GoBGP topology is not deployed.
func requireGoBGPLab(t *testing.T) {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), gobgpLabSpine) {
		t.Fatal("GoBGP topology not deployed (run: make clab-gobgp-up)")
	}
}

// gobgpDockerExec runs a command inside a GoBGP lab container.
func gobgpDockerExec(t *testing.T, container string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), bgpConvergeTimeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s %s failed: %v\n%s",
			container, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gobgpDockerExecRaw runs docker exec and returns output + error without failing.
func gobgpDockerExecRaw(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), bgpConvergeTimeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

// dumpDebugState collects comprehensive network and BGP debug info from all
// containers and logs it. Called automatically via t.Cleanup when a test fails.
// Inspired by das-schiff-network-operator E2E debug collection patterns.
func dumpDebugState(t *testing.T) {
	t.Helper()
	if !t.Failed() {
		return
	}

	t.Log("=== DEBUG STATE DUMP (test failed) ===")

	// Spine BGP and EVPN state (use JSON for structured output where possible).
	for _, cmd := range []string{
		"show bgp summary json",
		"show bgp l2vpn evpn",
		"show bgp l2vpn evpn json",
		"show ip route",
		"show evpn vni",
	} {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine, "vtysh", "-c", cmd)
		t.Logf("[spine01] %s:\n%s", cmd, out)
	}

	// Spine bridge/FDB/VXLAN state.
	for _, cmd := range [][]string{
		{"bridge", "fdb", "show"},
		{"ip", "-d", "link", "show", "type", "vxlan"},
		{"ip", "addr", "show"},
		{"ip", "neigh", "show"},
	} {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine, cmd...)
		t.Logf("[spine01] %s:\n%s", strings.Join(cmd, " "), out)
	}

	// RR01 BGP state.
	for _, cmd := range []string{
		"show bgp summary json",
		"show bgp neighbors 10.0.1.1 json",
	} {
		out, _ := gobgpDockerExecRaw(t, gobgpLabRR, "vtysh", "-c", cmd)
		t.Logf("[rr01] %s:\n%s", cmd, out)
	}

	// Network state from each BOOTy node.
	for _, node := range []struct{ name, container string }{
		{"booty-unnumbered", gobgpLabUnnumbered},
		{"booty-dual", gobgpLabDual},
		{"booty-numbered", gobgpLabNumbered},
	} {
		for _, cmd := range [][]string{
			{"ip", "-d", "link", "show", "type", "vxlan"},
			{"ip", "addr", "show"},
			{"ip", "-6", "addr", "show"},
			{"ip", "-6", "neigh", "show"},
			{"ip", "route", "show"},
			{"ip", "-6", "route", "show"},
			{"bridge", "fdb", "show"},
			{"ip", "neigh", "show"},
		} {
			out, _ := gobgpDockerExecRaw(t, node.container, cmd...)
			t.Logf("[%s] %s:\n%s", node.name, strings.Join(cmd, " "), out)
		}
	}

	// IPv6 neighbor table on spine (shows if BOOTy's link-local is known).
	for _, cmd := range [][]string{
		{"ip", "-6", "neigh", "show"},
		{"ip", "-6", "addr", "show"},
	} {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine, cmd...)
		t.Logf("[spine01] %s:\n%s", strings.Join(cmd, " "), out)
	}
}

// waitForBGPPeer polls the spine's FRR vtysh until the given neighbor reaches
// ESTABLISHED state. Uses per-neighbor JSON to avoid false positives from
// other sessions being established.
func waitForBGPPeer(t *testing.T, neighbor string) {
	t.Helper()
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show bgp neighbors "+neighbor+" json")
		if strings.Contains(out, "Established") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BGP peer %s did not reach ESTABLISHED within %s:\n%s",
				neighbor, bgpConvergeTimeout, out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// waitForBGPInterface waits for an interface-based peer to appear in the
// spine's BGP summary with Established state.
func waitForBGPInterface(t *testing.T, iface string) {
	t.Helper()
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show bgp neighbors "+iface+" json")
		if strings.Contains(out, "Established") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BGP peer on %s did not reach ESTABLISHED within %s:\n%s",
				iface, bgpConvergeTimeout, out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// --- Scenario 1: Unnumbered -------------------------------------------------

func TestGoBGPUnnumberedBGPEstablished(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// booty-unnumbered connects to spine on eth3 via eBGP unnumbered.
	waitForBGPInterface(t, "eth3")
	t.Log("Unnumbered BGP peer ESTABLISHED on spine:eth3")
}

func TestGoBGPUnnumberedEVPNActive(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	// Verify L2VPN-EVPN address family is negotiated.
	out := gobgpDockerExec(t, gobgpLabSpine,
		"vtysh", "-c", "show bgp neighbors eth3 json")
	if !strings.Contains(strings.ToLower(out), "l2vpnevpn") {
		t.Errorf("L2VPN-EVPN not active on unnumbered peer:\n%s", out)
	}
}

func TestGoBGPUnnumberedIPv4Active(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	out := gobgpDockerExec(t, gobgpLabSpine,
		"vtysh", "-c", "show bgp neighbors eth3 json")
	if !strings.Contains(out, "ipv4Unicast") {
		t.Errorf("IPv4 unicast not active on unnumbered peer:\n%s", out)
	}
}

func TestGoBGPUnnumberedUnderlayRoute(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	// BOOTy-unnumbered's underlay IP (10.0.0.20) should be in the spine's
	// BGP table, learned via the unnumbered session.
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show ip route 10.0.0.20/32")
		if strings.Contains(out, "10.0.0.20") {
			t.Log("Underlay route 10.0.0.20/32 present on spine")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("route 10.0.0.20/32 not learned on spine:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// --- Scenario 2: Dual -------------------------------------------------------

func TestGoBGPDualUnnumberedEstablished(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// booty-dual connects to spine on eth4 via eBGP unnumbered (IPv4 only).
	waitForBGPInterface(t, "eth4")
	t.Log("Dual mode: unnumbered BGP peer ESTABLISHED on spine:eth4")
}

func TestGoBGPDualNumberedEstablished(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// booty-dual also peers with rr01 via eBGP numbered for L2VPN-EVPN.
	// Check rr01's perspective: neighbor 10.0.3.2 should be ESTABLISHED.
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabRR,
			"vtysh", "-c", "show bgp neighbors 10.0.3.2 json")
		if strings.Contains(out, "Established") {
			t.Log("Dual mode: numbered eBGP peer ESTABLISHED on rr01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("eBGP peer 10.0.3.2 not ESTABLISHED on rr01:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

func TestGoBGPDualEVPNOnNumberedOnly(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// Wait for both sessions to come up.
	waitForBGPInterface(t, "eth4")

	// On the spine, the unnumbered peer (eth4) should have IPv4 unicast
	// but the EVPN session goes via the numbered peer to rr01.
	out := gobgpDockerExec(t, gobgpLabSpine,
		"vtysh", "-c", "show bgp neighbors eth4 json")
	if !strings.Contains(out, "ipv4Unicast") {
		t.Error("IPv4 unicast not active on dual mode unnumbered peer")
	}

	// Verify rr01 has L2VPN-EVPN active toward booty-dual.
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabRR,
			"vtysh", "-c", "show bgp neighbors 10.0.3.2 json")
		if strings.Contains(strings.ToLower(out), "l2vpnevpn") && strings.Contains(out, "Established") {
			t.Log("Dual mode: L2VPN-EVPN active on numbered eBGP to rr01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("L2VPN-EVPN not active on rr01 toward booty-dual:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// --- Scenario 3: Numbered ---------------------------------------------------

func TestGoBGPNumberedBGPEstablished(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// booty-numbered peers with spine via numbered eBGP (10.0.2.2 → 10.0.2.1).
	waitForBGPPeer(t, "10.0.2.2")
	t.Log("Numbered BGP peer ESTABLISHED on spine: 10.0.2.2")
}

func TestGoBGPNumberedEVPNActive(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPPeer(t, "10.0.2.2")

	out := gobgpDockerExec(t, gobgpLabSpine,
		"vtysh", "-c", "show bgp neighbors 10.0.2.2 json")
	if !strings.Contains(strings.ToLower(out), "l2vpnevpn") {
		t.Errorf("L2VPN-EVPN not active on numbered peer:\n%s", out)
	}
}

func TestGoBGPNumberedIPv4Active(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPPeer(t, "10.0.2.2")

	out := gobgpDockerExec(t, gobgpLabSpine,
		"vtysh", "-c", "show bgp neighbors 10.0.2.2 json")
	if !strings.Contains(out, "ipv4Unicast") {
		t.Errorf("IPv4 unicast not active on numbered peer:\n%s", out)
	}
}

func TestGoBGPNumberedUnderlayRoute(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPPeer(t, "10.0.2.2")

	// BOOTy-numbered's underlay IP (10.0.0.22) should be learned via the
	// numbered eBGP session.
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show ip route 10.0.0.22/32")
		if strings.Contains(out, "10.0.0.22") {
			t.Log("Underlay route 10.0.0.22/32 present on spine")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("route 10.0.0.22/32 not learned on spine:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// --- Cross-mode: fabric-level connectivity ----------------------------------

func TestGoBGPSpineLeafBGPEstablished(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// Spine ↔ leaf fabric link should always be up.
	waitForBGPInterface(t, "eth1")
	t.Log("Spine ↔ leaf fabric BGP ESTABLISHED")
}

func TestGoBGPRR01SpineBGPEstablished(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	// RR01 ↔ spine iBGP session (10.0.1.2 ↔ 10.0.1.1).
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabRR,
			"vtysh", "-c", "show bgp neighbors 10.0.1.1 json")
		if strings.Contains(out, "Established") {
			t.Log("RR01 ↔ spine iBGP ESTABLISHED")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("RR01→spine iBGP not ESTABLISHED:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// --- EVPN Data Plane --------------------------------------------------------

// TestGoBGPUnnumberedEVPNType5OnSpine verifies that the spine receives an EVPN
// Type-5 (IP Prefix) route from the unnumbered BOOTy node after BGP converges.
// BOOTy advertises its provision subnet so the fabric can route to it.
func TestGoBGPUnnumberedEVPNType5OnSpine(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show bgp l2vpn evpn")
		// BOOTy-unnumbered advertises its provision subnet (10.100.0.20).
		if strings.Contains(out, "10.100.0.20") {
			t.Log("EVPN Type-5 route from booty-unnumbered visible on spine")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("EVPN Type-5 route from booty-unnumbered not on spine:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPDualEVPNType5OnSpine verifies that the spine receives an EVPN
// Type-5 (IP Prefix) route from the dual-mode BOOTy node.
func TestGoBGPDualEVPNType5OnSpine(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPPeer(t, "10.0.3.2")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show bgp l2vpn evpn")
		// BOOTy-dual advertises its provision subnet (10.100.0.21).
		if strings.Contains(out, "10.100.0.21") {
			t.Log("EVPN Type-5 route from booty-dual visible on spine")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("EVPN Type-5 route from booty-dual not on spine:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPNumberedEVPNType5OnSpine verifies that the spine receives an EVPN
// Type-5 (IP Prefix) route from the numbered-mode BOOTy node.
func TestGoBGPNumberedEVPNType5OnSpine(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPPeer(t, "10.0.2.2")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabSpine,
			"vtysh", "-c", "show bgp l2vpn evpn")
		// BOOTy-numbered advertises its provision subnet (10.100.0.22).
		if strings.Contains(out, "10.100.0.22") {
			t.Log("EVPN Type-5 route from booty-numbered visible on spine")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("EVPN Type-5 route from booty-numbered not on spine:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPUnnumberedVXLANInterface verifies the VXLAN interface is created
// inside the booty-unnumbered container with the correct VNI.
func TestGoBGPUnnumberedVXLANInterface(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	// Allow time for overlay setup after BGP establishes.
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabUnnumbered, "ip", "-d", "link", "show", "type", "vxlan")
		if strings.Contains(out, "vxlan") && strings.Contains(out, "id 100") {
			t.Log("VXLAN interface with VNI 100 present on booty-unnumbered")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VXLAN interface not found on booty-unnumbered:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPUnnumberedBridgeProvisionIP verifies the provision IP is assigned
// on the bridge interface inside the booty-unnumbered container.
func TestGoBGPUnnumberedBridgeProvisionIP(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabUnnumbered, "ip", "addr", "show", "dev", "br.provision")
		if strings.Contains(out, "10.100.0.20") {
			t.Log("Provision IP 10.100.0.20 assigned on br.provision")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provision IP not on bridge:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPUnnumberedGatewayFDB verifies the BUM FDB entry for the gateway
// VTEP is installed on the VXLAN interface.
func TestGoBGPUnnumberedGatewayFDB(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabUnnumbered, "bridge", "fdb", "show", "dev", "vx100")
		if strings.Contains(out, "00:00:00:00:00:00") && strings.Contains(out, "10.0.0.1") {
			t.Log("Gateway BUM FDB entry present: 00:00:00:00:00:00 → 10.0.0.1")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BUM FDB entry for gateway not found:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPUnnumberedGatewayRoute verifies the /32 kernel route to the
// gateway VTEP is installed via the first physical NIC.
func TestGoBGPUnnumberedGatewayRoute(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, _ := gobgpDockerExecRaw(t, gobgpLabUnnumbered, "ip", "route", "show", "10.0.0.1/32")
		if strings.Contains(out, "10.0.0.1") {
			t.Log("Gateway route 10.0.0.1/32 present in kernel")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway /32 route not in kernel:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPUnnumberedVXLANPingGateway verifies end-to-end VXLAN data plane
// connectivity by pinging the spine's bridge IP (10.100.0.1) from inside
// the booty-unnumbered container over the VXLAN overlay.
func TestGoBGPUnnumberedVXLANPingGateway(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	// Give overlay stack time to fully initialize.
	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, err := gobgpDockerExecRaw(t, gobgpLabUnnumbered,
			"ping", "-c", "1", "-W", "2", "-I", "br.provision", "10.100.0.1")
		if err == nil && strings.Contains(out, "1 packets received") {
			t.Log("VXLAN overlay ping to gateway 10.100.0.1 succeeded")
			return
		}
		if time.Now().After(deadline) {
			// Dump diagnostic state before failing.
			routes, _ := gobgpDockerExecRaw(t, gobgpLabUnnumbered, "ip", "route")
			fdb, _ := gobgpDockerExecRaw(t, gobgpLabUnnumbered, "bridge", "fdb", "show")
			t.Fatalf("VXLAN ping to 10.100.0.1 failed after %s:\nping: %s\nroutes: %s\nfdb: %s",
				bgpConvergeTimeout, out, routes, fdb)
		}
		time.Sleep(bgpConvergeInterval)
	}
}

// TestGoBGPUnnumberedOverlayReachClient verifies that the booty-unnumbered
// node can reach the client container (10.100.0.100) through the VXLAN
// overlay via the spine's bridge.
func TestGoBGPUnnumberedOverlayReachClient(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")

	deadline := time.Now().Add(bgpConvergeTimeout)
	for {
		out, err := gobgpDockerExecRaw(t, gobgpLabUnnumbered,
			"ping", "-c", "1", "-W", "2", "-I", "br.provision", "10.100.0.100")
		if err == nil && strings.Contains(out, "1 packets received") {
			t.Log("Overlay connectivity to client 10.100.0.100 verified")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cannot reach client 10.100.0.100 from booty-unnumbered:\n%s", out)
		}
		time.Sleep(bgpConvergeInterval)
	}
}
