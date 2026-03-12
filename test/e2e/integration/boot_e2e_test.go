//go:build e2e_boot

// Package integration contains full BOOTy boot tests running inside
// containerlab. Three BOOTy instances (provision, deprovision, standby)
// run in parallel on the same EVPN fabric and talk to the CAPRF mock.
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
	labPrefix = "clab-booty-boot-lab"

	provisionContainer   = labPrefix + "-booty-provision"
	deprovisionContainer = labPrefix + "-booty-deprovision"
	standbyContainer     = labPrefix + "-booty-standby"
	caprfContainer       = labPrefix + "-caprf-mock"
	clientContainer      = labPrefix + "-client"
	spineContainer       = labPrefix + "-spine01"
	nginxContainer       = labPrefix + "-nginx"
)

// requireBootLab skips the test if the boot topology is not deployed.
func requireBootLab(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), provisionContainer) {
		t.Skip("Boot topology not deployed (" + provisionContainer + " not found)")
	}
}

func bootDockerExec(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

func bootDockerExecOrFail(t *testing.T, container string, args ...string) string {
	t.Helper()
	out, err := bootDockerExec(t, container, args...)
	if err != nil {
		t.Fatalf("docker exec %s %s failed: %v\n%s", container, strings.Join(args, " "), err, out)
	}
	return out
}

// getBootyLogs retrieves the full BOOTy log output from a container.
func getBootyLogs(t *testing.T, container string) string {
	t.Helper()
	// BOOTy output goes to container stdout/stderr
	out, err := exec.Command("docker", "logs", container).CombinedOutput()
	if err != nil {
		t.Logf("Warning: could not get logs for %s: %v", container, err)
		return ""
	}
	return string(out)
}

// waitForLogEntry waits until a log line appears in a container's output.
func waitForLogEntry(t *testing.T, container, entry string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		logs := getBootyLogs(t, container)
		if strings.Contains(logs, entry) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// waitForAccessLogEntry polls a container's file until it contains the expected string.
func waitForAccessLogEntry(t *testing.T, container, logPath, entry string, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastOut string
	for time.Now().Before(deadline) {
		out, err := bootDockerExec(t, container, "cat", logPath)
		if err == nil && strings.Contains(out, entry) {
			return out, true
		}
		lastOut = out
		time.Sleep(3 * time.Second)
	}
	return lastOut, false
}

// --- Connectivity tests: BOOTy nodes can reach services through fabric ---

func TestBootAllNodesReachCAPRF(t *testing.T) {
	requireBootLab(t)

	containers := []struct {
		name string
		desc string
	}{
		{provisionContainer, "provision"},
		{deprovisionContainer, "deprovision"},
		{standbyContainer, "standby"},
	}

	for _, c := range containers {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()
			// EVPN data-plane convergence can take several minutes in CI;
			// run all 3 connectivity checks in parallel with a generous budget.
			var reachable bool
			for i := 0; i < 420; i++ {
				_, err := bootDockerExec(t, c.name, "wget", "-q", "-O", "/dev/null", "--timeout=2", "http://10.100.0.11/health")
				if err == nil {
					reachable = true
					break
				}
				time.Sleep(1 * time.Second)
			}
			if !reachable {
				t.Fatalf("%s node cannot reach CAPRF mock (10.100.0.11) through EVPN fabric", c.desc)
			}
			t.Logf("%s node reaches CAPRF mock through EVPN fabric", c.desc)
		})
	}
}

func TestBootAllNodesReachNginx(t *testing.T) {
	requireBootLab(t)

	containers := []struct {
		name string
		desc string
	}{
		{provisionContainer, "provision"},
		{deprovisionContainer, "deprovision"},
		{standbyContainer, "standby"},
	}

	for _, c := range containers {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()
			var reachable bool
			for i := 0; i < 120; i++ {
				_, err := bootDockerExec(t, c.name, "wget", "-q", "-O", "/dev/null", "--timeout=2", "http://10.100.0.10/")
				if err == nil {
					reachable = true
					break
				}
				time.Sleep(1 * time.Second)
			}
			if !reachable {
				t.Fatalf("%s node cannot reach nginx (10.100.0.10) through EVPN fabric", c.desc)
			}
			t.Logf("%s node reaches nginx through EVPN fabric", c.desc)
		})
	}
}

// --- Full BOOTy boot log tests ---

func TestBootProvisionStartsAndReportsInit(t *testing.T) {
	requireBootLab(t)

	// Wait for BOOTy to produce its startup banner
	if !waitForLogEntry(t, provisionContainer, "Starting BOOTy", 60*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not start BOOTy within 60s\nFull logs:\n%s", logs)
	}

	// Wait for CAPRF mode detection
	if !waitForLogEntry(t, provisionContainer, "CAPRF mode active", 30*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not enter CAPRF mode\nFull logs:\n%s", logs)
	}

	// Verify FRR/EVPN network mode (not DHCP)
	if !waitForLogEntry(t, provisionContainer, "Using FRR/EVPN network mode", 30*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not enter FRR/EVPN network mode\nFull logs:\n%s", logs)
	}

	// Wait for provisioning to start (report-init step)
	// EVPN convergence (BGP + route exchange + VXLAN) takes 30-60s.
	if !waitForLogEntry(t, provisionContainer, "report-init", 120*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not reach report-init step\nFull logs:\n%s", logs)
	}

	t.Log("provision node: Started BOOTy → CAPRF mode → FRR/EVPN → report-init OK")
}

func TestBootDeprovisionStartsAndReportsInit(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, deprovisionContainer, "Starting BOOTy", 60*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node did not start BOOTy within 60s\nFull logs:\n%s", logs)
	}

	if !waitForLogEntry(t, deprovisionContainer, "CAPRF mode active", 30*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node did not enter CAPRF mode\nFull logs:\n%s", logs)
	}

	// Verify FRR/EVPN network mode (not DHCP)
	if !waitForLogEntry(t, deprovisionContainer, "Using FRR/EVPN network mode", 30*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node did not enter FRR/EVPN network mode\nFull logs:\n%s", logs)
	}

	t.Log("deprovision node: Started BOOTy → CAPRF mode → FRR/EVPN OK")
}

func TestBootStandbyEntersStandbyLoop(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, standbyContainer, "Starting BOOTy", 60*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node did not start BOOTy within 60s\nFull logs:\n%s", logs)
	}

	if !waitForLogEntry(t, standbyContainer, "CAPRF mode active", 30*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node did not enter CAPRF mode\nFull logs:\n%s", logs)
	}

	// Verify FRR/EVPN network mode (not DHCP)
	if !waitForLogEntry(t, standbyContainer, "Using FRR/EVPN network mode", 30*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node did not enter FRR/EVPN network mode\nFull logs:\n%s", logs)
	}

	// Standby mode should enter the standby loop
	if !waitForLogEntry(t, standbyContainer, "standby", 30*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node did not enter standby mode\nFull logs:\n%s", logs)
	}

	t.Log("standby node: Started BOOTy → CAPRF mode → FRR/EVPN → standby loop OK")
}

// --- Log content validation ---

func TestBootProvisionShowsHostname(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, provisionContainer, "booty-provision-e2e", 60*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node logs don't contain expected hostname\nFull logs:\n%s", logs)
	}
	t.Log("provision node: hostname appears in logs")
}

func TestBootDeprovisionShowsMode(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, deprovisionContainer, "mode=deprovision", 60*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node logs don't contain mode=deprovision\nFull logs:\n%s", logs)
	}
	t.Log("deprovision node: mode=deprovision appears in logs")
}

func TestBootStandbyShowsMode(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, standbyContainer, "mode=standby", 60*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node logs don't contain mode=standby\nFull logs:\n%s", logs)
	}
	t.Log("standby node: mode=standby appears in logs")
}

// --- CAPRF mock received requests ---

func TestBootCAPRFMockReceivedInitStatus(t *testing.T) {
	requireBootLab(t)

	out, ok := waitForAccessLogEntry(t, caprfContainer, "/var/log/nginx/access.log", "/status/init", 180*time.Second)
	if !ok {
		t.Fatalf("CAPRF mock did not receive /status/init request\nAccess log:\n%s", out)
	}
	t.Logf("CAPRF mock received /status/init request\nAccess log:\n%s", out)
}

// --- Image pull through EVPN ---

func TestBootAllNodesImageReachableThroughEVPN(t *testing.T) {
	requireBootLab(t)

	containers := []struct {
		name string
		desc string
	}{
		{provisionContainer, "provision"},
		{deprovisionContainer, "deprovision"},
		{standbyContainer, "standby"},
	}

	for _, c := range containers {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()
			var ok bool
			for i := 0; i < 30; i++ {
				_, err := bootDockerExec(t, c.name, "wget", "-q", "-O", "/dev/null", "http://10.100.0.10/images/test.img")
				if err == nil {
					ok = true
					break
				}
				time.Sleep(1 * time.Second)
			}
			if !ok {
				t.Fatalf("%s node cannot download image from nginx (10.100.0.10) through EVPN", c.desc)
			}
			t.Logf("%s node: image download from nginx through EVPN succeeded", c.desc)
		})
	}
}

func TestBootNginxAccessLogShowsImageRequest(t *testing.T) {
	requireBootLab(t)

	out, ok := waitForAccessLogEntry(t, nginxContainer, "/var/log/nginx/access.log", "/images/test.img", 60*time.Second)
	if !ok {
		t.Logf("Nginx access log:\n%s", out)
		t.Fatal("nginx did not receive /images/test.img request through EVPN")
	}
	t.Logf("Nginx received image request through EVPN:\n%s", out)
}

// --- CAPRF error lifecycle (provision fails at disk ops) ---

func TestBootCAPRFMockReceivedErrorFromProvision(t *testing.T) {
	requireBootLab(t)

	out, ok := waitForAccessLogEntry(t, caprfContainer, "/var/log/nginx/access.log", "/status/error", 90*time.Second)
	if !ok {
		t.Logf("CAPRF access log:\n%s", out)
		t.Fatal("CAPRF mock did not receive /status/error — provision should fail at disk ops")
	}
	t.Log("CAPRF mock received /status/error (provision failed at disk ops as expected)")
}

func TestBootProvisionShowsProvisioningSteps(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, provisionContainer, "report-init", 60*time.Second) {
		t.Fatal("provision node did not reach report-init")
	}

	// Wait for provisioning steps to execute and (likely) fail at disk ops
	time.Sleep(15 * time.Second)

	logs := getBootyLogs(t, provisionContainer)

	// Provisioning should log step names and eventually fail
	if strings.Contains(logs, "Provisioning step") || strings.Contains(logs, "detect-disk") {
		t.Log("provision node: provisioning steps visible in logs (full orchestrator lifecycle)")
	} else {
		t.Logf("Full logs:\n%s", logs)
		t.Fatal("provision node: no provisioning step activity found in logs")
	}

	// If a disk is available (e.g. loop device), provisioning may progress past detect-disk.
	if strings.Contains(logs, "stream-image") || strings.Contains(logs, "Using configured disk device") {
		t.Log("provision node: provisioning progressed past disk detection")
	} else {
		t.Log("provision node: provisioning did not reach stream-image (disk may not be available)")
	}
}

// --- Standby heartbeat through EVPN ---

func TestBootStandbyHeartbeatsSentToCAPRF(t *testing.T) {
	requireBootLab(t)

	// Wait for standby to enter heartbeat loop
	if !waitForLogEntry(t, standbyContainer, "standby", 60*time.Second) {
		t.Fatal("standby node did not enter standby mode")
	}

	out, ok := waitForAccessLogEntry(t, caprfContainer, "/var/log/nginx/access.log", "/status/heartbeat", 60*time.Second)
	if !ok {
		t.Logf("CAPRF access log:\n%s", out)
		t.Fatal("CAPRF mock did not receive /status/heartbeat from standby")
	}
	t.Log("CAPRF mock received heartbeat from standby node through EVPN")
}

// --- Unexpected ERROR detection ---

// allowedErrorPatterns lists error messages that are expected in minimal CI
// environments (no real disk, provisioning failure at disk ops, etc.).
var allowedErrorPatterns = []string{
	"no suitable disk found",
	"detect-disk",
	"Connecting to provisioning server",
	"DEBUG DUMP",
	"=== DEBUG",
	"=== CONFIG",
	"Provisioning failed",
	"Provisioning step",
	"Deprovisioning failed",
	"Deprovisioning step",
	"stream-image",
	"partition-disk",
	"parse-partitions",
	"format-disk",
	"Disk Error",
	"msg=DEBUG",
	"Debug command",
	"DEBUG env",
}

func TestBootNoUnexpectedErrors(t *testing.T) {
	requireBootLab(t)

	// Wait for BOOTy to have progressed through provisioning attempt
	time.Sleep(15 * time.Second)

	containers := []struct {
		name string
		desc string
	}{
		{provisionContainer, "provision"},
		{deprovisionContainer, "deprovision"},
		{standbyContainer, "standby"},
	}

	for _, c := range containers {
		logs := getBootyLogs(t, c.name)
		for _, line := range strings.Split(logs, "\n") {
			if !strings.Contains(line, "level=ERROR") {
				continue
			}
			allowed := false
			for _, pattern := range allowedErrorPatterns {
				if strings.Contains(line, pattern) {
					allowed = true
					break
				}
			}
			if !allowed {
				t.Errorf("%s: unexpected ERROR log: %s", c.desc, line)
			}
		}
	}
}

// --- Full log dump test (always runs last) ---

func TestBootZZZDumpAllLogs(t *testing.T) {
	requireBootLab(t)

	// Wait for BOOTy processes to have run
	time.Sleep(10 * time.Second)

	containers := []struct {
		name string
		desc string
	}{
		{provisionContainer, "PROVISION"},
		{deprovisionContainer, "DEPROVISION"},
		{standbyContainer, "STANDBY"},
	}

	for _, c := range containers {
		logs := getBootyLogs(t, c.name)
		t.Logf("\n========================================\n"+
			"  %s NODE FULL BOOTY LOGS\n"+
			"========================================\n%s\n"+
			"========================================\n",
			c.desc, logs)
	}

	// Also dump CAPRF mock logs
	accessLog, _ := bootDockerExec(t, caprfContainer, "cat", "/var/log/nginx/access.log")
	t.Logf("\n========================================\n"+
		"  CAPRF MOCK ACCESS LOG\n"+
		"========================================\n%s\n"+
		"========================================\n",
		accessLog)

	// Dump BGP state
	bgpSummary := bootDockerExecOrFail(t, spineContainer, "vtysh", "-c", "show bgp summary")
	t.Logf("\n========================================\n"+
		"  BGP SUMMARY (spine01)\n"+
		"========================================\n%s\n"+
		"========================================\n",
		bgpSummary)

	// Dump EVPN state
	evpnState, _ := bootDockerExec(t, spineContainer, "vtysh", "-c", "show bgp l2vpn evpn")
	t.Logf("\n========================================\n"+
		"  EVPN STATE (spine01)\n"+
		"========================================\n%s\n"+
		"========================================\n",
		evpnState)

	fmt.Println("All BOOTy boot logs dumped above")
}
