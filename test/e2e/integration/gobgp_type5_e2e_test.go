//go:build e2e_gobgp_type5

// Package integration contains E2E tests for the pure Type-5 per-machine leaf fabric.
// These tests validate that BOOTy's GoBGP stack works in the exact CAPRF production layout:
//   - Per-machine leaf (pure L3, no VXLAN on leaf)
//   - eBGP unnumbered peering between BOOTy and its dedicated leaf
//   - L3VNI 1000 with nolearning (EVPN control-plane driven)
//   - Pure Type-5 IP prefix routes (NO Type-2/Type-3)
//   - Jumbo MTU 9100 on underlay
//
// Prerequisites:
//
//	make clab-type5-up   # deploys topology-type5.clab.yml
package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	type5LabSpine = "clab-booty-type5-lab-spine01"
	type5LabLeaf1 = "clab-booty-type5-lab-leaf01"
	type5LabLeaf2 = "clab-booty-type5-lab-leaf02"
	type5LabVM0   = "clab-booty-type5-lab-booty-vm0"
	type5LabVM1   = "clab-booty-type5-lab-booty-vm1"

	type5ConvergeTimeout  = 90 * time.Second
	type5ConvergeInterval = 2 * time.Second
)

func requireType5Lab(t *testing.T) {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), type5LabSpine) {
		t.Fatal("Type-5 topology not deployed (run: make clab-type5-up)")
	}
}

func type5DockerExec(t *testing.T, container string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), type5ConvergeTimeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s %s failed: %v\n%s",
			container, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func type5DockerExecRaw(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), type5ConvergeTimeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

func type5Vtysh(t *testing.T, container string, cmd string) string {
	t.Helper()
	return type5DockerExec(t, container, "vtysh", "-c", cmd)
}

func type5VtyshRaw(t *testing.T, container string, cmd string) (string, error) {
	t.Helper()
	return type5DockerExecRaw(t, container, "vtysh", "-c", cmd)
}

func type5DumpDebugState(t *testing.T) {
	t.Helper()
	if !t.Failed() {
		return
	}

	t.Log("=== TYPE-5 DEBUG STATE DUMP (test failed) ===")

	for _, cmd := range []string{
		"show bgp summary json",
		"show bgp ipv4 unicast",
		"show bgp l2vpn evpn",
		"show bgp l2vpn evpn route type prefix",
		"show bgp l2vpn evpn route type macip",
		"show bgp l2vpn evpn route type multicast",
		"show evpn vni",
		"show ip route",
	} {
		out, _ := type5VtyshRaw(t, type5LabSpine, cmd)
		t.Logf("[spine01] %s:\n%s", cmd, out)
	}

	for _, cmd := range [][]string{
		{"ip", "-d", "link", "show", "type", "vxlan"},
		{"ip", "addr", "show"},
		{"bridge", "fdb", "show"},
	} {
		out, _ := type5DockerExecRaw(t, type5LabSpine, cmd...)
		t.Logf("[spine01] %s:\n%s", strings.Join(cmd, " "), out)
	}

	for _, leaf := range []struct {
		name, container string
	}{
		{"leaf01", type5LabLeaf1},
		{"leaf02", type5LabLeaf2},
	} {
		for _, cmd := range []string{
			"show bgp summary json",
			"show bgp l2vpn evpn",
			"show ip route",
		} {
			out, _ := type5VtyshRaw(t, leaf.container, cmd)
			t.Logf("[%s] %s:\n%s", leaf.name, cmd, out)
		}
	}

	for _, vm := range []struct {
		name, container string
	}{
		{"booty-vm0", type5LabVM0},
		{"booty-vm1", type5LabVM1},
	} {
		for _, cmd := range [][]string{
			{"ip", "-d", "link", "show", "type", "vxlan"},
			{"ip", "addr", "show"},
			{"ip", "route", "show"},
			{"bridge", "fdb", "show"},
		} {
			out, _ := type5DockerExecRaw(t, vm.container, cmd...)
			t.Logf("[%s] %s:\n%s", vm.name, strings.Join(cmd, " "), out)
		}
	}
}

func type5WaitForBGPInterface(t *testing.T, container, iface string) {
	t.Helper()
	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5VtyshRaw(t, container, "show bgp neighbors "+iface+" json")
		if strings.Contains(out, "Established") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BGP peer on %s (%s) did not reach ESTABLISHED within %s:\n%s",
				container, iface, type5ConvergeTimeout, out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}


// --- Spine-Leaf Fabric BGP ---------------------------------------------------

func TestType5SpineLeaf01BGPEstablished(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth1")
	t.Log("spine01 ↔ leaf01 BGP ESTABLISHED")
}

func TestType5SpineLeaf02BGPEstablished(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth2")
	t.Log("spine01 ↔ leaf02 BGP ESTABLISHED")
}

func TestType5Leaf01VM0BGPEstablished(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")
	t.Log("leaf01 ↔ booty-vm0 BGP ESTABLISHED (proves GoBGP unnumbered)")
}

func TestType5Leaf02VM1BGPEstablished(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf2, "eth2")
	t.Log("leaf02 ↔ booty-vm1 BGP ESTABLISHED (proves GoBGP unnumbered)")
}

// --- EVPN Address Family Active ----------------------------------------------

func TestType5Leaf01EVPNActive(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")

	out := type5Vtysh(t, type5LabLeaf1, "show bgp neighbors eth2 json")
	if !strings.Contains(strings.ToLower(out), "l2vpnevpn") {
		t.Errorf("L2VPN-EVPN not active on leaf01:eth2 (toward VM0):\n%s", out)
	}
}

func TestType5Leaf02EVPNActive(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf2, "eth2")

	out := type5Vtysh(t, type5LabLeaf2, "show bgp neighbors eth2 json")
	if !strings.Contains(strings.ToLower(out), "l2vpnevpn") {
		t.Errorf("L2VPN-EVPN not active on leaf02:eth2 (toward VM1):\n%s", out)
	}
}

// --- Underlay VTEP Reachability ----------------------------------------------

func TestType5VM0VTEPReachable(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth1")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5VtyshRaw(t, type5LabSpine,
			"show bgp ipv4 unicast 192.168.4.10/32")
		if strings.Contains(out, "192.168.4.10") && !strings.Contains(out, "not in table") {
			t.Log("VM0 VTEP 192.168.4.10/32 learned on spine01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VM0 VTEP 192.168.4.10/32 not on spine01:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VM1VTEPReachable(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5VtyshRaw(t, type5LabSpine,
			"show bgp ipv4 unicast 192.168.4.11/32")
		if strings.Contains(out, "192.168.4.11") && !strings.Contains(out, "not in table") {
			t.Log("VM1 VTEP 192.168.4.11/32 learned on spine01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VM1 VTEP 192.168.4.11/32 not on spine01:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

// --- Type-5 Routes on Spine --------------------------------------------------

func TestType5VM0OverlayAsType5(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth1")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5VtyshRaw(t, type5LabSpine,
			"show bgp l2vpn evpn route type prefix")
		if strings.Contains(out, "10.200.0.10") {
			t.Log("VM0 overlay IP 10.200.0.10 present as Type-5 on spine01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VM0 overlay Type-5 route not on spine01:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VM1OverlayAsType5(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5VtyshRaw(t, type5LabSpine,
			"show bgp l2vpn evpn route type prefix")
		if strings.Contains(out, "10.200.0.11") {
			t.Log("VM1 overlay IP 10.200.0.11 present as Type-5 on spine01")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VM1 overlay Type-5 route not on spine01:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

// --- NO Type-2 (MAC/IP) Routes -----------------------------------------------

func TestType5NoType2Routes(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth1")
	type5WaitForBGPInterface(t, type5LabSpine, "eth2")

	out, _ := type5VtyshRaw(t, type5LabSpine,
		"show bgp l2vpn evpn route type macip")
	if strings.Contains(out, "10.200.0") {
		t.Errorf("pure Type-5 model should have NO Type-2 routes for VM overlay IPs:\n%s", out)
	}
	t.Log("Confirmed: no Type-2 (MAC/IP) routes for VM overlay IPs on spine01")
}

// --- NO Type-3 (IMET) Routes from VMs ----------------------------------------

func TestType5NoType3RoutesFromVMs(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabSpine, "eth1")
	type5WaitForBGPInterface(t, type5LabSpine, "eth2")

	out, _ := type5VtyshRaw(t, type5LabSpine,
		"show bgp l2vpn evpn route type multicast")
	if strings.Contains(out, "192.168.4.10") || strings.Contains(out, "192.168.4.11") {
		t.Errorf("pure Type-5 model: VMs should NOT send Type-3 (IMET) routes:\n%s", out)
	}
	t.Log("Confirmed: no Type-3 (IMET) routes from VM VTEPs on spine01")
}

// --- Spine Configuration Correctness -----------------------------------------

func TestType5SpineAdvertiseIPv4Unicast(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	out := type5Vtysh(t, type5LabSpine, "show running-config")
	if !strings.Contains(out, "advertise ipv4 unicast") {
		t.Error("spine01 should have 'advertise ipv4 unicast' for Type-5 generation")
	}
	if !strings.Contains(out, "advertise-all-vni") {
		t.Error("spine01 should have 'advertise-all-vni' for EVPN VNI discovery")
	}
	t.Log("Confirmed: spine01 uses advertise ipv4 unicast + advertise-all-vni")
}

// --- Leaves are Pure L3 (No VXLAN) -------------------------------------------

func TestType5LeavesPureL3(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	for _, leaf := range []struct {
		name, container string
	}{
		{"leaf01", type5LabLeaf1},
		{"leaf02", type5LabLeaf2},
	} {
		out, _ := type5DockerExecRaw(t, leaf.container, "ip", "-d", "link", "show", "type", "vxlan")
		if strings.Contains(out, "vxlan") {
			t.Errorf("%s has VXLAN interfaces — should be pure L3 router:\n%s", leaf.name, out)
		}
	}
	t.Log("Confirmed: leaves are pure L3 routers (no VXLAN)")
}

// --- VXLAN Data Plane --------------------------------------------------------

func TestType5VXLANOnVM0(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5DockerExecRaw(t, type5LabVM0, "ip", "-d", "link", "show", "type", "vxlan")
		if strings.Contains(out, "vxlan") && strings.Contains(out, "id 1000") {
			t.Log("VXLAN interface with VNI 1000 present on booty-vm0")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VXLAN VNI 1000 not found on booty-vm0:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VXLANOnVM1(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf2, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5DockerExecRaw(t, type5LabVM1, "ip", "-d", "link", "show", "type", "vxlan")
		if strings.Contains(out, "vxlan") && strings.Contains(out, "id 1000") {
			t.Log("VXLAN interface with VNI 1000 present on booty-vm1")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("VXLAN VNI 1000 not found on booty-vm1:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VXLANNolearningOnSpine(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	out := type5DockerExec(t, type5LabSpine, "ip", "-d", "link", "show", "dev", "vxlan1000")
	if !strings.Contains(out, "nolearning") {
		t.Errorf("spine01 vxlan1000 should have nolearning flag:\n%s", out)
	}
	if !strings.Contains(out, "id 1000") {
		t.Errorf("spine01 vxlan1000 should have VNI 1000:\n%s", out)
	}
	t.Log("Confirmed: spine01 vxlan1000 has nolearning + VNI 1000")
}

func TestType5SpineFDBHasVMVTEPs(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	out := type5DockerExec(t, type5LabSpine, "bridge", "fdb", "show", "dev", "vxlan1000")
	if !strings.Contains(out, "192.168.4.10") {
		t.Errorf("spine01 FDB missing VM0 VTEP 192.168.4.10:\n%s", out)
	}
	if !strings.Contains(out, "192.168.4.11") {
		t.Errorf("spine01 FDB missing VM1 VTEP 192.168.4.11:\n%s", out)
	}
	t.Log("Confirmed: spine01 FDB has BUM entries for VM VTEPs")
}

// --- Overlay Connectivity ----------------------------------------------------

func TestType5VM0ProvisionIP(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5DockerExecRaw(t, type5LabVM0, "ip", "addr", "show", "dev", "br.provision")
		if strings.Contains(out, "10.200.0.10") {
			t.Log("Provision IP 10.200.0.10 assigned on booty-vm0 br.provision")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provision IP 10.200.0.10 not on br.provision:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VM1ProvisionIP(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf2, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5DockerExecRaw(t, type5LabVM1, "ip", "addr", "show", "dev", "br.provision")
		if strings.Contains(out, "10.200.0.11") {
			t.Log("Provision IP 10.200.0.11 assigned on booty-vm1 br.provision")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provision IP 10.200.0.11 not on br.provision:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VM0GatewayFDB(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5DockerExecRaw(t, type5LabVM0, "bridge", "fdb", "show")
		if strings.Contains(out, "00:00:00:00:00:00") && strings.Contains(out, "192.168.4.1") {
			t.Log("Gateway BUM FDB entry present on VM0: 00:00:00:00:00:00 → 192.168.4.1")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BUM FDB entry for spine VTEP not found on VM0:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5VM0GatewayRoute(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, _ := type5DockerExecRaw(t, type5LabVM0, "ip", "route", "show")
		if strings.Contains(out, "192.168.4.1") {
			t.Log("Gateway route to 192.168.4.1 present in VM0 kernel")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway route to 192.168.4.1 not installed on VM0:\n%s", out)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5PingGatewayFromVM0(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf1, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, err := type5DockerExecRaw(t, type5LabVM0,
			"ping", "-c", "1", "-W", "2", "-I", "br.provision", "10.200.0.1")
		if err == nil && strings.Contains(out, "1 packets received") {
			t.Log("VXLAN overlay ping from VM0 to spine gateway 10.200.0.1 succeeded")
			return
		}
		if time.Now().After(deadline) {
			routes, _ := type5DockerExecRaw(t, type5LabVM0, "ip", "route")
			fdb, _ := type5DockerExecRaw(t, type5LabVM0, "bridge", "fdb", "show")
			t.Fatalf("overlay ping to 10.200.0.1 failed after %s:\nping: %s\nroutes: %s\nfdb: %s",
				type5ConvergeTimeout, out, routes, fdb)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

func TestType5PingGatewayFromVM1(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	type5WaitForBGPInterface(t, type5LabLeaf2, "eth2")

	deadline := time.Now().Add(type5ConvergeTimeout)
	for {
		out, err := type5DockerExecRaw(t, type5LabVM1,
			"ping", "-c", "1", "-W", "2", "-I", "br.provision", "10.200.0.1")
		if err == nil && strings.Contains(out, "1 packets received") {
			t.Log("VXLAN overlay ping from VM1 to spine gateway 10.200.0.1 succeeded")
			return
		}
		if time.Now().After(deadline) {
			routes, _ := type5DockerExecRaw(t, type5LabVM1, "ip", "route")
			fdb, _ := type5DockerExecRaw(t, type5LabVM1, "bridge", "fdb", "show")
			t.Fatalf("overlay ping to 10.200.0.1 failed after %s:\nping: %s\nroutes: %s\nfdb: %s",
				type5ConvergeTimeout, out, routes, fdb)
		}
		time.Sleep(type5ConvergeInterval)
	}
}

// --- Jumbo MTU ---------------------------------------------------------------

func TestType5JumboMTU(t *testing.T) {
	requireType5Lab(t)
	t.Cleanup(func() { type5DumpDebugState(t) })

	// Spine → leaf interfaces should be MTU 9100.
	out := type5DockerExec(t, type5LabSpine, "ip", "link", "show", "dev", "eth1")
	if !strings.Contains(out, "mtu 9100") {
		t.Errorf("spine01:eth1 should have MTU 9100:\n%s", out)
	}
	out = type5DockerExec(t, type5LabSpine, "ip", "link", "show", "dev", "eth2")
	if !strings.Contains(out, "mtu 9100") {
		t.Errorf("spine01:eth2 should have MTU 9100:\n%s", out)
	}

	// Leaf → VM interfaces should be MTU 9100.
	out = type5DockerExec(t, type5LabLeaf1, "ip", "link", "show", "dev", "eth2")
	if !strings.Contains(out, "mtu 9100") {
		t.Errorf("leaf01:eth2 should have MTU 9100:\n%s", out)
	}
	out = type5DockerExec(t, type5LabLeaf2, "ip", "link", "show", "dev", "eth2")
	if !strings.Contains(out, "mtu 9100") {
		t.Errorf("leaf02:eth2 should have MTU 9100:\n%s", out)
	}

	t.Log("Confirmed: jumbo MTU 9100 on all underlay interfaces")
}

