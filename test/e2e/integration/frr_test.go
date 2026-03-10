//go:build e2e_integration

// Package integration contains E2E integration tests that run against
// ContainerLab topology and mock Redfish server.
package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// requireContainerLab skips the test if ContainerLab containers are not running.
func requireContainerLab(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), "clab-booty-lab-spine01") {
		t.Skip("ContainerLab topology not deployed (clab-booty-lab-spine01 not found)")
	}
}

// dockerExec runs a command inside a ContainerLab container.
func dockerExec(t *testing.T, container string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s %s failed: %v\n%s", container, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestBGPPeerEstablished(t *testing.T) {
	requireContainerLab(t)

	// Give BGP time to converge.
	var established bool
	for range 30 {
		out := dockerExec(t, "clab-booty-lab-spine01", "vtysh", "-c", "show bgp summary")
		if strings.Contains(out, "Estab") {
			established = true
			t.Logf("BGP established:\n%s", out)
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !established {
		out := dockerExec(t, "clab-booty-lab-spine01", "vtysh", "-c", "show bgp summary")
		t.Fatalf("BGP peers not established after 30s:\n%s", out)
	}
}

func TestBGPEVPNAddressFamily(t *testing.T) {
	requireContainerLab(t)

	// Verify EVPN address family is active on spine.
	out := dockerExec(t, "clab-booty-lab-spine01", "vtysh", "-c", "show bgp l2vpn evpn summary")
	if !strings.Contains(out, "Estab") {
		t.Fatalf("EVPN address family not established:\n%s", out)
	}
	t.Logf("EVPN summary:\n%s", out)
}

func TestLeafLoopbackReachable(t *testing.T) {
	requireContainerLab(t)

	// Wait for route propagation, then verify spine can see leaf's loopback.
	var found bool
	for range 15 {
		out := dockerExec(t, "clab-booty-lab-spine01", "vtysh", "-c", "show ip route")
		if strings.Contains(out, "10.0.0.2") {
			found = true
			t.Logf("Leaf loopback visible in spine routing table")
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !found {
		out := dockerExec(t, "clab-booty-lab-spine01", "vtysh", "-c", "show ip route")
		t.Fatalf("leaf loopback 10.0.0.2 not found in spine routing table:\n%s", out)
	}
}

func TestSpineLoopbackReachable(t *testing.T) {
	requireContainerLab(t)

	// Verify leaf can see spine's loopback.
	var found bool
	for range 15 {
		out := dockerExec(t, "clab-booty-lab-leaf01", "vtysh", "-c", "show ip route")
		if strings.Contains(out, "10.0.0.1") {
			found = true
			t.Logf("Spine loopback visible in leaf routing table")
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !found {
		out := dockerExec(t, "clab-booty-lab-leaf01", "vtysh", "-c", "show ip route")
		t.Fatalf("spine loopback 10.0.0.1 not found in leaf routing table:\n%s", out)
	}
}
