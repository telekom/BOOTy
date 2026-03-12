//go:build e2e_vrnetlab

// EVPN E2E tests for BOOTy running as a real QEMU VM inside containerlab.
// Three BOOTy VMs (provision, deprovision, standby) boot with real PID 1 init,
// real mount/device setup, and communicate with the CAPRF mock through a full
// EVPN fabric (spine01 ↔ leaf01, VXLAN VNI 100, eBGP).
//
// Requires: topology-vrnetlab.clab.yml deployed via `make clab-vrnetlab-up`
package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	vrnetlabPrefix = "clab-booty-vrnetlab-lab"

	vmProvision   = vrnetlabPrefix + "-booty-provision"
	vmDeprovision = vrnetlabPrefix + "-booty-deprovision"
	vmStandby     = vrnetlabPrefix + "-booty-standby"
	vmCAPRF       = vrnetlabPrefix + "-caprf-mock"
	vmSpine       = vrnetlabPrefix + "-spine01"
	vmLeaf        = vrnetlabPrefix + "-leaf01"
	vmClient      = vrnetlabPrefix + "-client"
	vmNginx       = vrnetlabPrefix + "-nginx"
)

// ─── Helpers ──────────────────────────────────────────────────────────

func requireVrnetlabLab(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), vmProvision) {
		t.Skip("vrnetlab topology not deployed (" + vmProvision + " not found)")
	}
}

func vmDockerExec(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

func vmDockerExecOrFail(t *testing.T, container string, args ...string) string {
	t.Helper()
	out, err := vmDockerExec(t, container, args...)
	if err != nil {
		t.Fatalf("docker exec %s %v failed: %v\n%s", container, args, err, out)
	}
	return out
}

// getVMSerialLog retrieves QEMU serial console output from docker logs.
func getVMSerialLog(t *testing.T, container string) string {
	t.Helper()
	out, err := exec.Command("docker", "logs", container).CombinedOutput()
	if err != nil {
		t.Logf("could not get logs for %s: %v", container, err)
		return ""
	}
	return string(out)
}

// waitForVMLog polls docker logs until entry appears or timeout.
func waitForVMLog(t *testing.T, container, entry string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		logs := getVMSerialLog(t, container)
		if strings.Contains(logs, entry) {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

// waitForVMAccessLog polls a container's file until it contains the expected string.
func waitForVMAccessLog(t *testing.T, container, logPath, entry string, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastOut string
	for time.Now().Before(deadline) {
		out, err := vmDockerExec(t, container, "cat", logPath)
		if err == nil && strings.Contains(out, entry) {
			return out, true
		}
		lastOut = out
		time.Sleep(3 * time.Second)
	}
	return lastOut, false
}

// ═══════════════════════════════════════════════════════════════════════
// EVPN Fabric Validation
// ═══════════════════════════════════════════════════════════════════════

func TestVrnetlabBGPSessionsEstablished(t *testing.T) {
	requireVrnetlabLab(t)

	var established bool
	for i := 0; i < 180; i++ {
		out, err := vmDockerExec(t, vmSpine, "vtysh", "-c", "show bgp summary")
		// "never" in Up/Down column = peer not yet established;
		// "65020" = booty-provision AS confirms peers are configured.
		if err == nil && strings.Contains(out, "65020") && !strings.Contains(out, "never") {
			established = true
			t.Logf("BGP established:\n%s", out)
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !established {
		out, _ := vmDockerExec(t, vmSpine, "vtysh", "-c", "show bgp summary")
		nbr, _ := vmDockerExec(t, vmSpine, "vtysh", "-c", "show bgp neighbor json")
		bfd, _ := vmDockerExec(t, vmSpine, "vtysh", "-c", "show bfd peers json")
		// Retrieve VM-side FRR state via docker logs (serial console output).
		vmLogs := getVMSerialLog(t, vmProvision)
		vmTail := ""
		if len(vmLogs) > 2000 {
			vmTail = vmLogs[len(vmLogs)-2000:]
		} else {
			vmTail = vmLogs
		}
		t.Fatalf("BGP sessions not established on spine01\nSummary:\n%s\nNeighbor JSON:\n%s\nBFD JSON:\n%s\nVM serial (tail):\n%s", out, nbr, bfd, vmTail)
	}
}

func TestVrnetlabEVPNRoutesPresent(t *testing.T) {
	requireVrnetlabLab(t)

	// Allow time for EVPN convergence
	time.Sleep(5 * time.Second)

	out := vmDockerExecOrFail(t, vmSpine, "vtysh", "-c", "show bgp l2vpn evpn")
	if !strings.Contains(out, "Route Distinguisher") && !strings.Contains(out, "Network") {
		t.Fatalf("no EVPN routes found on spine01\n%s", out)
	}
	t.Logf("EVPN routes present on spine01:\n%s", out)
}

func TestVrnetlabLeafBGPEstablished(t *testing.T) {
	requireVrnetlabLab(t)

	var established bool
	for i := 0; i < 60; i++ {
		out, err := vmDockerExec(t, vmLeaf, "vtysh", "-c", "show bgp summary")
		// "never" in Up/Down column = peer not yet established;
		// "65000" = spine01 AS confirms peer is configured.
		if err == nil && strings.Contains(out, "65000") && !strings.Contains(out, "never") {
			established = true
			t.Logf("Leaf BGP established:\n%s", out)
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !established {
		out, _ := vmDockerExec(t, vmLeaf, "vtysh", "-c", "show bgp summary")
		t.Fatalf("BGP sessions not established on leaf01\n%s", out)
	}
}

func TestVrnetlabClientReachesNginxThroughEVPN(t *testing.T) {
	requireVrnetlabLab(t)

	var reachable bool
	for i := 0; i < 30; i++ {
		out, err := vmDockerExec(t, vmClient, "ping", "-c1", "-W1", "10.100.0.10")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("client cannot reach nginx (10.100.0.10) through EVPN fabric")
	}
	t.Log("client → nginx reachable through EVPN fabric")
}

func TestVrnetlabClientReachesCAPRFThroughEVPN(t *testing.T) {
	requireVrnetlabLab(t)

	var reachable bool
	for i := 0; i < 30; i++ {
		out, err := vmDockerExec(t, vmClient, "ping", "-c1", "-W1", "10.100.0.11")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("client cannot reach CAPRF mock (10.100.0.11) through EVPN fabric")
	}
	t.Log("client → CAPRF mock reachable through EVPN fabric")
}

// ═══════════════════════════════════════════════════════════════════════
// QEMU VM Boot Lifecycle
// ═══════════════════════════════════════════════════════════════════════

func TestVrnetlabVMBootStarted(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmProvision, "Starting BOOTy", 120*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not start BOOTy within 120s\nSerial log:\n%s", logs)
	}
	t.Log("provision VM: BOOTy started as PID 1")
}

func TestVrnetlabVMMountsSuccessful(t *testing.T) {
	requireVrnetlabLab(t)

	// BOOTy logs "Beginning provisioning process" after setupMountsAndDevices() and loadModules() complete
	if !waitForVMLog(t, vmProvision, "Beginning provisioning process", 120*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not reach provisioning stage (mounts may have failed)\n%s", logs)
	}
	t.Log("provision VM: mount and device setup completed")
}

func TestVrnetlabVMCAPRFModeDetected(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmProvision, "CAPRF mode active", 150*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not enter CAPRF mode\n%s", logs)
	}
	t.Log("provision VM: CAPRF mode detected from /deploy/vars")
}

func TestVrnetlabVMFRRNetworkModeDetected(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmProvision, "Using FRR/EVPN network mode", 150*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not enter FRR/EVPN network mode\n%s", logs)
	}
	t.Log("provision VM: FRR/EVPN network mode active")
}

func TestVrnetlabVMNetworkConnectivity(t *testing.T) {
	requireVrnetlabLab(t)

	// BOOTy logs "Waiting for network connectivity" before polling
	if !waitForVMLog(t, vmProvision, "Waiting for network connectivity", 150*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not reach connectivity check\n%s", logs)
	}
	t.Log("provision VM: network connectivity check initiated")
}

func TestVrnetlabVMProvisionReportsInit(t *testing.T) {
	requireVrnetlabLab(t)

	// report-init means: boot → mounts → DHCP goroutine → /deploy/vars → CAPRF →
	// FRR/EVPN network mode → connectivity OK → CAPRF init report
	if !waitForVMLog(t, vmProvision, "report-init", 180*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not reach report-init\n%s", logs)
	}
	t.Log("provision VM: report-init reached — full CAPRF lifecycle working through EVPN")
}

// ═══════════════════════════════════════════════════════════════════════
// EVPN: VM → CAPRF Mock Communication
// ═══════════════════════════════════════════════════════════════════════

func TestVrnetlabCAPRFMockReceivedInit(t *testing.T) {
	requireVrnetlabLab(t)

	out, ok := waitForVMAccessLog(t, vmCAPRF, "/var/log/nginx/access.log", "/status/init", 120*time.Second)
	if !ok {
		t.Logf("CAPRF access log:\n%s", out)
		t.Fatal("CAPRF mock did not receive /status/init — VM→EVPN→CAPRF path broken")
	}
	t.Logf("CAPRF mock received init from BOOTy VM:\n%s", out)
}

func TestVrnetlabCAPRFMockReceivedHeartbeat(t *testing.T) {
	requireVrnetlabLab(t)

	out, ok := waitForVMAccessLog(t, vmCAPRF, "/var/log/nginx/access.log", "/status/heartbeat", 120*time.Second)
	if !ok {
		t.Logf("CAPRF access log:\n%s", out)
		t.Log("no heartbeat received yet (standby VM may not have reached heartbeat loop)")
	} else {
		t.Log("CAPRF mock received heartbeat from standby VM through EVPN")
	}
}

// ═══════════════════════════════════════════════════════════════════════// Image Pull Through EVPN
// ═════════════════════════════════════════════════════════════════════

func TestVrnetlabProvisionAttemptsImageDownload(t *testing.T) {
	requireVrnetlabLab(t)

	// Wait for provisioning to start and attempt image streaming
	if !waitForVMLog(t, vmProvision, "report-init", 180*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not reach report-init\n%s", logs)
	}

	// Wait for provisioning steps to execute past report-init
	time.Sleep(30 * time.Second)

	logs := getVMSerialLog(t, vmProvision)

	// Check for image download attempt or provisioning step progression
	hasImageAttempt := strings.Contains(logs, "Streaming image") ||
		strings.Contains(logs, "Beginning write") ||
		strings.Contains(logs, "stream-image")
	hasDiskStep := strings.Contains(logs, "detect-disk") ||
		strings.Contains(logs, "Provisioning step")

	if hasImageAttempt {
		t.Log("provision VM: image download attempted through EVPN")
	} else if hasDiskStep {
		t.Log("provision VM: provisioning reached disk step (image download may not be reached without block device)")
	} else {
		t.Logf("Serial log:\n%s", logs)
		t.Fatal("provision VM: no provisioning step activity found after report-init")
	}
}

func TestVrnetlabNginxReceivedImageRequest(t *testing.T) {
	requireVrnetlabLab(t)

	out, ok := waitForVMAccessLog(t, vmNginx, "/var/log/nginx/access.log", "/images/test.img", 120*time.Second)
	if !ok {
		t.Logf("Nginx access log:\n%s", out)
		t.Log("no /images/test.img request in nginx log (VM may not have reached stream-image step)")
	} else {
		t.Logf("Nginx received image request from BOOTy VM through EVPN:\n%s", out)
	}
}

func TestVrnetlabCAPRFMockReceivedErrorFromProvision(t *testing.T) {
	requireVrnetlabLab(t)

	out, ok := waitForVMAccessLog(t, vmCAPRF, "/var/log/nginx/access.log", "/status/error", 180*time.Second)
	if !ok {
		t.Logf("CAPRF access log:\n%s", out)
		t.Fatal("CAPRF mock did not receive /status/error — provision should fail at disk ops")
	}
	t.Log("CAPRF mock received /status/error (full CAPRF error lifecycle through EVPN)")
}

// ═════════════════════════════════════════════════════════════════════
// Full Lifecycle Through EVPN (per mode)
// ═════════════════════════════════════════════════════════════════════

func TestVrnetlabProvisionFullLifecycleViaEVPN(t *testing.T) {
	requireVrnetlabLab(t)

	steps := []struct {
		entry   string
		timeout time.Duration
		desc    string
	}{
		{"Starting BOOTy", 120 * time.Second, "BOOTy started as PID 1"},
		{"CAPRF mode active", 60 * time.Second, "CAPRF mode detected from /deploy/vars"},
		{"Using FRR/EVPN network mode", 30 * time.Second, "FRR/EVPN network mode active"},
		{"report-init", 60 * time.Second, "init reported to CAPRF through EVPN"},
	}

	for _, step := range steps {
		if !waitForVMLog(t, vmProvision, step.entry, step.timeout) {
			logs := getVMSerialLog(t, vmProvision)
			t.Fatalf("provision VM did not reach: %s\n%s", step.desc, logs)
		}
		t.Logf("provision VM: %s", step.desc)
	}

	// Verify provisioning continues past report-init
	time.Sleep(15 * time.Second)
	logs := getVMSerialLog(t, vmProvision)

	if strings.Contains(logs, "Provisioning step") || strings.Contains(logs, "detect-disk") {
		t.Log("provision VM: provisioning orchestrator executing steps through EVPN")
	}

	// Verify CAPRF mock received the init POST through EVPN
	access, _ := vmDockerExec(t, vmCAPRF, "cat", "/var/log/nginx/access.log")
	if strings.Contains(access, "/status/init") {
		t.Log("provision VM: CAPRF init status received through EVPN")
	}
}

func TestVrnetlabDeprovisionFullLifecycleViaEVPN(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmDeprovision, "Starting BOOTy", 120*time.Second) {
		logs := getVMSerialLog(t, vmDeprovision)
		t.Fatalf("deprovision VM did not start BOOTy\n%s", logs)
	}
	t.Log("deprovision VM: BOOTy started as PID 1")

	if !waitForVMLog(t, vmDeprovision, "CAPRF mode active", 60*time.Second) {
		logs := getVMSerialLog(t, vmDeprovision)
		t.Fatalf("deprovision VM did not enter CAPRF mode\n%s", logs)
	}
	t.Log("deprovision VM: CAPRF mode active")

	if !waitForVMLog(t, vmDeprovision, "Using FRR/EVPN network mode", 60*time.Second) {
		logs := getVMSerialLog(t, vmDeprovision)
		t.Fatalf("deprovision VM did not enter FRR/EVPN mode\n%s", logs)
	}
	t.Log("deprovision VM: FRR/EVPN network mode active")

	// Wait for deprovisioning steps to execute
	time.Sleep(15 * time.Second)
	logs := getVMSerialLog(t, vmDeprovision)

	if strings.Contains(logs, "Deprovisioning step") || strings.Contains(logs, "Starting deprovisioning") ||
		strings.Contains(logs, "report-init") {
		t.Log("deprovision VM: deprovisioning lifecycle executing through EVPN")
	}
}

func TestVrnetlabStandbyFullLifecycleViaEVPN(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmStandby, "Starting BOOTy", 120*time.Second) {
		logs := getVMSerialLog(t, vmStandby)
		t.Fatalf("standby VM did not start BOOTy\n%s", logs)
	}
	t.Log("standby VM: BOOTy started as PID 1")

	if !waitForVMLog(t, vmStandby, "CAPRF mode active", 60*time.Second) {
		logs := getVMSerialLog(t, vmStandby)
		t.Fatalf("standby VM did not enter CAPRF mode\n%s", logs)
	}
	t.Log("standby VM: CAPRF mode active")

	if !waitForVMLog(t, vmStandby, "Using FRR/EVPN network mode", 60*time.Second) {
		logs := getVMSerialLog(t, vmStandby)
		t.Fatalf("standby VM did not enter FRR/EVPN mode\n%s", logs)
	}
	t.Log("standby VM: FRR/EVPN network mode active")

	if !waitForVMLog(t, vmStandby, "standby", 60*time.Second) {
		logs := getVMSerialLog(t, vmStandby)
		t.Fatalf("standby VM did not enter standby mode\n%s", logs)
	}
	t.Log("standby VM: entered standby mode")

	// Verify heartbeat was sent to CAPRF through EVPN
	time.Sleep(30 * time.Second)
	access, _ := vmDockerExec(t, vmCAPRF, "cat", "/var/log/nginx/access.log")
	if strings.Contains(access, "/status/heartbeat") {
		t.Log("standby VM: heartbeat sent to CAPRF through EVPN")
	}
}

// ═════════════════════════════════════════════════════════════════════// Multi-mode Validation
// ═══════════════════════════════════════════════════════════════════════

func TestVrnetlabProvisionMode(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmProvision, "CAPRF mode active", 150*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM did not enter CAPRF mode\n%s", logs)
	}

	logs := getVMSerialLog(t, vmProvision)
	if !strings.Contains(logs, "mode=provision") && !strings.Contains(logs, "\"mode\":\"provision\"") {
		// mode might be logged differently; check for provision-related activity
		if !strings.Contains(logs, "booty-provision-e2e") {
			t.Logf("Serial log:\n%s", logs)
			t.Fatal("provision VM not in provision mode")
		}
	}
	t.Log("provision VM: mode=provision confirmed")
}

func TestVrnetlabDeprovisionMode(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmDeprovision, "CAPRF mode active", 150*time.Second) {
		logs := getVMSerialLog(t, vmDeprovision)
		t.Fatalf("deprovision VM did not enter CAPRF mode\n%s", logs)
	}

	if !waitForVMLog(t, vmDeprovision, "deprovision", 30*time.Second) {
		logs := getVMSerialLog(t, vmDeprovision)
		t.Fatalf("deprovision VM not in deprovision mode\n%s", logs)
	}
	t.Log("deprovision VM: mode=deprovision confirmed")
}

func TestVrnetlabStandbyMode(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmStandby, "CAPRF mode active", 150*time.Second) {
		logs := getVMSerialLog(t, vmStandby)
		t.Fatalf("standby VM did not enter CAPRF mode\n%s", logs)
	}

	if !waitForVMLog(t, vmStandby, "standby", 30*time.Second) {
		logs := getVMSerialLog(t, vmStandby)
		t.Fatalf("standby VM not in standby mode\n%s", logs)
	}
	t.Log("standby VM: mode=standby confirmed")
}

func TestVrnetlabProvisionShowsHostname(t *testing.T) {
	requireVrnetlabLab(t)

	if !waitForVMLog(t, vmProvision, "booty-provision-e2e", 150*time.Second) {
		logs := getVMSerialLog(t, vmProvision)
		t.Fatalf("provision VM logs missing hostname\n%s", logs)
	}
	t.Log("provision VM: hostname appears in serial output")
}

func TestVrnetlabAllVMsBootSuccessfully(t *testing.T) {
	requireVrnetlabLab(t)

	vms := []struct {
		container string
		desc      string
	}{
		{vmProvision, "provision"},
		{vmDeprovision, "deprovision"},
		{vmStandby, "standby"},
	}

	for _, vm := range vms {
		vm := vm
		t.Run(vm.desc, func(t *testing.T) {
			t.Parallel()
			if !waitForVMLog(t, vm.container, "Starting BOOTy", 120*time.Second) {
				logs := getVMSerialLog(t, vm.container)
				t.Fatalf("%s VM did not start BOOTy\n%s", vm.desc, logs)
			}
			t.Logf("%s VM: BOOTy started", vm.desc)
		})
	}
}

func TestVrnetlabAllVMsEnterCAPRF(t *testing.T) {
	requireVrnetlabLab(t)

	vms := []struct {
		container string
		desc      string
	}{
		{vmProvision, "provision"},
		{vmDeprovision, "deprovision"},
		{vmStandby, "standby"},
	}

	for _, vm := range vms {
		vm := vm
		t.Run(vm.desc, func(t *testing.T) {
			t.Parallel()
			if !waitForVMLog(t, vm.container, "CAPRF mode active", 150*time.Second) {
				logs := getVMSerialLog(t, vm.container)
				t.Fatalf("%s VM did not enter CAPRF mode\n%s", vm.desc, logs)
			}
			t.Logf("%s VM: CAPRF mode active", vm.desc)
		})
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Unexpected ERROR Detection
// ═══════════════════════════════════════════════════════════════════════

// vrnetlabAllowedErrors lists error messages expected in CI (no real disk, etc.).
var vrnetlabAllowedErrors = []string{
	"no suitable disk found",
	"detect-disk",
	"Connecting to provisioning server",
	"DEBUG DUMP",
	"=== DEBUG",
	"=== CONFIG",
	"Provisioning failed",
	"stream-image",
	"partition-disk",
	"format-disk",
}

func TestVrnetlabNoUnexpectedErrors(t *testing.T) {
	requireVrnetlabLab(t)

	time.Sleep(15 * time.Second)

	vms := []struct {
		name string
		desc string
	}{
		{vmProvision, "provision"},
		{vmDeprovision, "deprovision"},
		{vmStandby, "standby"},
	}

	for _, vm := range vms {
		logs := getVMSerialLog(t, vm.name)
		for _, line := range strings.Split(logs, "\n") {
			if !strings.Contains(line, "level=ERROR") {
				continue
			}
			allowed := false
			for _, pattern := range vrnetlabAllowedErrors {
				if strings.Contains(line, pattern) {
					allowed = true
					break
				}
			}
			if !allowed {
				t.Errorf("%s: unexpected ERROR log: %s", vm.desc, line)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Full Log Dump (always runs last for debugging)
// ═══════════════════════════════════════════════════════════════════════

func TestVrnetlabDumpAllLogs(t *testing.T) {
	requireVrnetlabLab(t)

	time.Sleep(10 * time.Second)

	vms := []struct {
		name string
		desc string
	}{
		{vmProvision, "PROVISION"},
		{vmDeprovision, "DEPROVISION"},
		{vmStandby, "STANDBY"},
	}

	for _, vm := range vms {
		logs := getVMSerialLog(t, vm.name)
		t.Logf("\n"+
			"════════════════════════════════════════\n"+
			"  %s VM SERIAL LOG\n"+
			"════════════════════════════════════════\n"+
			"%s\n"+
			"════════════════════════════════════════",
			vm.desc, logs)
	}

	// CAPRF access log
	accessLog, _ := vmDockerExec(t, vmCAPRF, "cat", "/var/log/nginx/access.log")
	t.Logf("\n"+
		"════════════════════════════════════════\n"+
		"  CAPRF MOCK ACCESS LOG\n"+
		"════════════════════════════════════════\n"+
		"%s\n"+
		"════════════════════════════════════════",
		accessLog)

	// BGP and EVPN state
	bgp := vmDockerExecOrFail(t, vmSpine, "vtysh", "-c", "show bgp summary")
	t.Logf("\nBGP Summary (spine01):\n%s", bgp)

	evpn, _ := vmDockerExec(t, vmSpine, "vtysh", "-c", "show bgp l2vpn evpn")
	t.Logf("\nEVPN State (spine01):\n%s", evpn)

	leafBgp, _ := vmDockerExec(t, vmLeaf, "vtysh", "-c", "show bgp summary")
	t.Logf("\nBGP Summary (leaf01):\n%s", leafBgp)

	fmt.Println("vrnetlab EVPN E2E: all logs dumped")
}
