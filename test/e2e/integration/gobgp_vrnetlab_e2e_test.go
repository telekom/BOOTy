//go:build e2e_gobgp_vrnetlab

// Package integration contains E2E tests for GoBGP PeerMode scenarios using
// QEMU VMs via vrnetlab.  BOOTy runs as real PID 1 with GoBGP (no FRR).
//
// Prerequisites:
//
//	make clab-gobgp-vrnetlab-up
package integration

import (
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
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), gobgpVRSpine) {
		t.Skip("GoBGP vrnetlab topology not deployed (run: make clab-gobgp-vrnetlab-up)")
	}
}

func vrDockerExec(t *testing.T, container string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s %s failed: %v\n%s",
			container, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func vrDockerExecRaw(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	return string(out), err
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
	vrWaitForBGPInterface(t, "eth3")
	t.Log("VM unnumbered: BGP ESTABLISHED on spine:eth3")
}

func TestVRGoBGPUnnumberedEVPN(t *testing.T) {
	requireGoBGPVRLab(t)
	vrWaitForBGPInterface(t, "eth3")

	out := vrDockerExec(t, gobgpVRSpine,
		"vtysh", "-c", "show bgp neighbors eth3 json")
	if !strings.Contains(out, "l2vpnEvpn") {
		t.Errorf("L2VPN-EVPN not active on unnumbered VM peer:\n%s", out)
	}
}

// --- Dual VM boot -----------------------------------------------------------

func TestVRGoBGPDualUnnumberedEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	vrWaitForBGPInterface(t, "eth4")
	t.Log("VM dual: unnumbered BGP ESTABLISHED on spine:eth4")
}

func TestVRGoBGPDualNumberedEstablished(t *testing.T) {
	requireGoBGPVRLab(t)

	deadline := time.Now().Add(vrBGPTimeout)
	for {
		out, _ := vrDockerExecRaw(t, gobgpVRRR,
			"vtysh", "-c", "show bgp neighbors 10.0.3.2 json")
		if strings.Contains(out, "Established") {
			t.Log("VM dual: numbered iBGP ESTABLISHED on rr01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VM dual iBGP peer not ESTABLISHED on rr01:\n%s", out)
		}
		time.Sleep(vrBGPPollInterval)
	}
}

func TestVRGoBGPDualEVPNOnRR(t *testing.T) {
	requireGoBGPVRLab(t)

	deadline := time.Now().Add(vrBGPTimeout)
	for {
		out, _ := vrDockerExecRaw(t, gobgpVRRR,
			"vtysh", "-c", "show bgp neighbors 10.0.3.2 json")
		if strings.Contains(out, "l2vpnEvpn") && strings.Contains(out, "Established") {
			t.Log("VM dual: L2VPN-EVPN active on numbered iBGP to rr01")
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
	vrWaitForBGPPeer(t, "10.0.2.2")
	t.Log("VM numbered: BGP ESTABLISHED on spine: 10.0.2.2")
}

func TestVRGoBGPNumberedEVPN(t *testing.T) {
	requireGoBGPVRLab(t)
	vrWaitForBGPPeer(t, "10.0.2.2")

	out := vrDockerExec(t, gobgpVRSpine,
		"vtysh", "-c", "show bgp neighbors 10.0.2.2 json")
	if !strings.Contains(out, "l2vpnEvpn") {
		t.Errorf("L2VPN-EVPN not active on numbered VM peer:\n%s", out)
	}
}

// --- Fabric health ----------------------------------------------------------

func TestVRGoBGPFabricEstablished(t *testing.T) {
	requireGoBGPVRLab(t)
	vrWaitForBGPInterface(t, "eth1")
	t.Log("VM fabric: spine ↔ leaf ESTABLISHED")
}
