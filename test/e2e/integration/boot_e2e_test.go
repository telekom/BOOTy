//go:build e2e_boot

// Package integration contains full BOOTy boot tests running inside
// containerlab. Three BOOTy instances (provision, deprovision, standby)
// run in parallel on the same EVPN fabric and talk to the CAPRF mock.
package integration

import (
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
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
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
	// BOOTy output goes to container stdout/stderr via tee
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

// --- Connectivity tests: BOOTy nodes can reach services through fabric ---

func TestBootProvisionNodeReachesCAPRF(t *testing.T) {
	requireBootLab(t)

	// The provision node (10.100.0.20) should reach caprf-mock (10.100.0.11) through EVPN.
	var reachable bool
	for i := 0; i < 30; i++ {
		out, err := bootDockerExec(t, provisionContainer, "ping", "-c", "1", "-W", "1", "10.100.0.11")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("provision node cannot reach CAPRF mock (10.100.0.11) through EVPN fabric")
	}
	t.Log("provision node reaches CAPRF mock through EVPN fabric")
}

func TestBootDeprovisionNodeReachesCAPRF(t *testing.T) {
	requireBootLab(t)

	var reachable bool
	for i := 0; i < 30; i++ {
		out, err := bootDockerExec(t, deprovisionContainer, "ping", "-c", "1", "-W", "1", "10.100.0.11")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("deprovision node cannot reach CAPRF mock (10.100.0.11) through EVPN fabric")
	}
	t.Log("deprovision node reaches CAPRF mock through EVPN fabric")
}

func TestBootStandbyNodeReachesCAPRF(t *testing.T) {
	requireBootLab(t)

	var reachable bool
	for i := 0; i < 30; i++ {
		out, err := bootDockerExec(t, standbyContainer, "ping", "-c", "1", "-W", "1", "10.100.0.11")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("standby node cannot reach CAPRF mock (10.100.0.11) through EVPN fabric")
	}
	t.Log("standby node reaches CAPRF mock through EVPN fabric")
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
			for i := 0; i < 30; i++ {
				out, err := bootDockerExec(t, c.name, "ping", "-c", "1", "-W", "1", "10.100.0.10")
				if err == nil && strings.Contains(out, "1 packets received") {
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

	// Wait for provisioning to start (report-init step)
	if !waitForLogEntry(t, provisionContainer, "report-init", 30*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not reach report-init step\nFull logs:\n%s", logs)
	}

	t.Log("provision node: Started BOOTy → CAPRF mode → report-init OK")
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

	t.Log("deprovision node: Started BOOTy → CAPRF mode OK")
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

	// Standby mode should enter the standby loop
	if !waitForLogEntry(t, standbyContainer, "standby", 30*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node did not enter standby mode\nFull logs:\n%s", logs)
	}

	t.Log("standby node: Started BOOTy → CAPRF mode → standby loop OK")
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

	// Wait for at least one BOOTy node to report init
	time.Sleep(30 * time.Second)

	// Check CAPRF mock access log for /status/init requests
	out, err := bootDockerExec(t, caprfContainer, "cat", "/var/log/nginx/access.log")
	if err != nil {
		t.Fatalf("could not read CAPRF access log: %v\n%s", err, out)
	}

	if !strings.Contains(out, "/status/init") {
		t.Fatalf("CAPRF mock did not receive /status/init request\nAccess log:\n%s", out)
	}
	t.Logf("CAPRF mock received /status/init request\nAccess log:\n%s", out)
}

// --- Full log dump test (always runs last) ---

func TestBootDumpAllLogs(t *testing.T) {
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
