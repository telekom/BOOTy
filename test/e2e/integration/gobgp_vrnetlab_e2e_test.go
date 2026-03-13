//go:build e2e_gobgp_vrnetlab

// Package integration contains E2E tests for GoBGP PeerMode scenarios using
// QEMU VMs via vrnetlab.  BOOTy runs as real PID 1 with GoBGP (no FRR).
//
// Prerequisites:
//
//	make clab-gobgp-vrnetlab-up
package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	gobgpVRSpine      = "clab-booty-gobgp-vrnetlab-lab-spine01"
	gobgpVRRR         = "clab-booty-gobgp-vrnetlab-lab-rr01"
	gobgpVRUnnumbered = "clab-booty-gobgp-vrnetlab-lab-booty-unnumbered"
	gobgpVRDual       = "clab-booty-gobgp-vrnetlab-lab-booty-dual"
	gobgpVRNumbered   = "clab-booty-gobgp-vrnetlab-lab-booty-numbered"
	vrBGPTimeout      = 120 * time.Second
	vrBGPPollInterval = 3 * time.Second
)

func requireGoBGPVRLab(t *testing.T) {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), gobgpVRSpine) {
		t.Skip("GoBGP vrnetlab topology not deployed (run: make clab-gobgp-vrnetlab-up)")
	}
}

func vrDockerExec(t *testing.T, container string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), vrBGPTimeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s %s failed: %v\n%s",
			container, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func vrDockerExecRaw(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), vrBGPTimeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

// vrDumpDebugState collects comprehensive network and BGP debug info from all
// vrnetlab containers and logs it. Called via t.Cleanup on test failure.
func vrDumpDebugState(t *testing.T) {
	t.Helper()
	if !t.Failed() {
		return
	}

	t.Log("=== DEBUG STATE DUMP (vrnetlab test failed) ===")

	for _, cmd := range []string{
		"show bgp summary",
		"show bgp neighbors eth3 json",
		"show bgp neighbors eth4 json",
		"show bgp neighbors 10.0.2.2 json",
		"show bgp l2vpn evpn",
		"show ip route",
	} {
		out, _ := vrDockerExecRaw(t, gobgpVRSpine, "vtysh", "-c", cmd)
		t.Logf("[spine01] %s:\n%s", cmd, out)
	}

	for _, cmd := range []string{
		"show bgp summary",
		"show bgp neighbors 10.0.3.2 json",
	} {
		out, _ := vrDockerExecRaw(t, gobgpVRRR, "vtysh", "-c", cmd)
		t.Logf("[rr01] %s:\n%s", cmd, out)
	}

	for _, node := range []struct{ name, container string }{
		{"booty-unnumbered", gobgpVRUnnumbered},
		{"booty-dual", gobgpVRDual},
		{"booty-numbered", gobgpVRNumbered},
	} {
		for _, cmd := range [][]string{
			{"ip", "-6", "addr", "show"},
			{"ip", "-6", "neigh", "show"},
			{"ip", "route", "show"},
			{"ip", "-6", "route", "show"},
		} {
			out, _ := vrDockerExecRaw(t, node.container, cmd...)
			t.Logf("[%s] %s:\n%s", node.name, strings.Join(cmd, " "), out)
		}
	}

	for _, cmd := range [][]string{
		{"ip", "-6", "neigh", "show"},
		{"ip", "-6", "addr", "show"},
	} {
		out, _ := vrDockerExecRaw(t, gobgpVRSpine, cmd...)
		t.Logf("[spine01] %s:\n%s", strings.Join(cmd, " "), out)
	}
}

func vrWaitForBGPInterface(t *testing.T, iface string) {
	t.Helper()
	deadline := time.Now().Add(vrBGPTimeout)
	for {
		out, _ := vrDockerExecRaw(t, gobgpVRSpine,
			"vtysh", "-c", "show bgp neighbors "+iface+" json")
		if strings.Contains(out, "Established") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BGP peer on %s did not establish within %s:\n%s",
				iface, vrBGPTimeout, out)
		}
		time.Sleep(vrBGPPollInterval)
	}
}

func vrWaitForBGPPeer(t *testing.T, neighbor string) {
	t.Helper()
	deadline := time.Now().Add(vrBGPTimeout)
	for {
		out, _ := vrDockerExecRaw(t, gobgpVRSpine,
			"vtysh", "-c", "show bgp neighbors "+neighbor+" json")
		if strings.Contains(out, "Established") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BGP peer %s did not establish within %s:\n%s",
				neighbor, vrBGPTimeout, out)
		}
		time.Sleep(vrBGPPollInterval)
	}
}

// --- Unnumbered VM boot -----------------------------------------------------

func TestVRGoBGPUnnumberedEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })
	vrWaitForBGPInterface(t, "eth3")
	t.Log("VM unnumbered: BGP ESTABLISHED on spine:eth3")
}

func TestVRGoBGPUnnumberedEVPN(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })
	vrWaitForBGPInterface(t, "eth3")

	out := vrDockerExec(t, gobgpVRSpine,
		"vtysh", "-c", "show bgp neighbors eth3 json")
	if !strings.Contains(strings.ToLower(out), "l2vpnevpn") {
		t.Errorf("L2VPN-EVPN not active on unnumbered VM peer:\n%s", out)
	}
}

// --- Dual VM boot -----------------------------------------------------------

func TestVRGoBGPDualUnnumberedEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })
	vrWaitForBGPInterface(t, "eth4")
	t.Log("VM dual: unnumbered BGP ESTABLISHED on spine:eth4")
}

func TestVRGoBGPDualNumberedEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })

	deadline := time.Now().Add(vrBGPTimeout)
	for {
		out, _ := vrDockerExecRaw(t, gobgpVRRR,
			"vtysh", "-c", "show bgp neighbors 10.0.3.2 json")
		if strings.Contains(out, "Established") {
			t.Log("VM dual: numbered eBGP ESTABLISHED on rr01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VM dual eBGP peer not ESTABLISHED on rr01:\n%s", out)
		}
		time.Sleep(vrBGPPollInterval)
	}
}

func TestVRGoBGPDualEVPNOnRR(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })

	deadline := time.Now().Add(vrBGPTimeout)
	for {
		out, _ := vrDockerExecRaw(t, gobgpVRRR,
			"vtysh", "-c", "show bgp neighbors 10.0.3.2 json")
		if strings.Contains(strings.ToLower(out), "l2vpnevpn") && strings.Contains(out, "Established") {
			t.Log("VM dual: L2VPN-EVPN active on numbered eBGP to rr01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("L2VPN-EVPN not active on rr01 toward VM booty-dual:\n%s", out)
		}
		time.Sleep(vrBGPPollInterval)
	}
}

// --- Numbered VM boot -------------------------------------------------------

func TestVRGoBGPNumberedEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })
	vrWaitForBGPPeer(t, "10.0.2.2")
	t.Log("VM numbered: BGP ESTABLISHED on spine: 10.0.2.2")
}

func TestVRGoBGPNumberedEVPN(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })
	vrWaitForBGPPeer(t, "10.0.2.2")

	out := vrDockerExec(t, gobgpVRSpine,
		"vtysh", "-c", "show bgp neighbors 10.0.2.2 json")
	if !strings.Contains(strings.ToLower(out), "l2vpnevpn") {
		t.Errorf("L2VPN-EVPN not active on numbered VM peer:\n%s", out)
	}
}

// --- Fabric health ----------------------------------------------------------

func TestVRGoBGPFabricEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	t.Cleanup(func() { vrDumpDebugState(t) })
	vrWaitForBGPInterface(t, "eth1")
	t.Log("VM fabric: spine ↔ leaf ESTABLISHED")
}
