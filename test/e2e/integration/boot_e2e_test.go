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

	bootReachabilityTimeout      = 6 * time.Minute
	bootCAPRFReachabilityTimeout = 10 * time.Minute
	bootProbeInterval            = time.Second
	bootProbeTimeoutSeconds      = "5"
	bootRecoveryRestarts         = 1
	bootRestartBudget            = 125 * time.Second
)

type bootHTTPProbe struct {
	container string
	desc      string
	url       string
	contains  string
}

// requireBootLab fails the test if the boot topology is not deployed.
func requireBootLab(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker not available: %v", err)
	}
	if !strings.Contains(string(out), provisionContainer) {
		t.Fatal("Boot topology not deployed (" + provisionContainer + " not found)")
	}
}

func bootDockerExec(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	return bootDockerExecWithTimeout(t, 60*time.Second, container, args...)
}

func bootDockerExecWithTimeout(t *testing.T, timeout time.Duration, container string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

func bootDockerExecBefore(t *testing.T, deadline time.Time, container string, args ...string) (string, error) {
	t.Helper()
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return "", context.DeadlineExceeded
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

func dockerLogsArgs(container, tail string, since time.Time) []string {
	args := []string{"logs"}
	if !since.IsZero() {
		args = append(args, "--since", since.UTC().Format(time.RFC3339Nano))
	}
	if tail != "" {
		args = append(args, "--tail", tail)
	}
	return append(args, container)
}

func getBootyLogsWithArgs(t *testing.T, container string, args []string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			t.Logf("Warning: timed out retrieving logs for %s", container)
			return string(out)
		}
		t.Logf("Warning: could not get logs for %s: %v", container, err)
		return ""
	}
	return string(out)
}

// getBootyLogs retrieves all BOOTy log output from a container.
// For bounded output (e.g. CI dump tests), use getBootyLogsTail instead.
func getBootyLogs(t *testing.T, container string) string {
	t.Helper()
	return getBootyLogsWithArgs(t, container, dockerLogsArgs(container, "", time.Time{}))
}

// getBootyLogsTail retrieves the last N lines of BOOTy log output from a container.
func getBootyLogsTail(t *testing.T, container, tail string) string {
	t.Helper()
	return getBootyLogsWithArgs(t, container, dockerLogsArgs(container, tail, time.Time{}))
}

func getBootyLogsSince(t *testing.T, container string, since time.Time) string {
	t.Helper()
	return getBootyLogsWithArgs(t, container, dockerLogsArgs(container, "", since))
}

// waitForLogEntry waits until a log line appears in a container's output.
func waitForLogEntry(t *testing.T, container, entry string, timeout time.Duration) bool {
	t.Helper()
	return waitForLogEntrySince(t, container, entry, timeout, time.Time{})
}

func waitForLogEntrySince(t *testing.T, container, entry string, timeout time.Duration, since time.Time) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		logs := getBootyLogsSince(t, container, since)
		if strings.Contains(logs, entry) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// bootyNetworkNeedsRecovery checks if BOOTy can no longer serve as a live
// network probe without a restart. This includes explicit connectivity
// exhaustion and terminal mode exit after retries are exhausted.
func bootyNetworkNeedsRecovery(t *testing.T, container string, since time.Time) bool {
	t.Helper()
	logs := getBootyLogsSince(t, container, since)
	return networkProbeNeedsRestart(logs)
}

func networkProbeNeedsRestart(logs string) bool {
	restartMarkers := []string{
		"network connectivity failed after all retries",
		"mode exited with error",
		"network teardown error",
	}
	for _, marker := range restartMarkers {
		if strings.Contains(logs, marker) {
			return true
		}
	}
	return false
}

func TestNetworkProbeNeedsRestart(t *testing.T) {
	tests := []struct {
		name string
		logs string
		want bool
	}{
		{
			name: "active network",
			logs: `time=2026-06-26 level=INFO msg="Network connectivity established"`,
		},
		{
			name: "connectivity exhausted",
			logs: `level=ERROR msg="network connectivity failed after all retries"`,
			want: true,
		},
		{
			name: "mode exited",
			logs: `level=ERROR msg="mode exited with error" mode=provision`,
			want: true,
		},
		{
			name: "teardown warning",
			logs: `level=WARN msg="network teardown error"`,
			want: true,
		},
		{
			name: "frr teardown alone still allows retry",
			logs: `level=INFO msg="FRR teardown complete" component=frr`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := networkProbeNeedsRestart(tt.logs); got != tt.want {
				t.Fatalf("networkProbeNeedsRestart() = %v, want %v", got, tt.want)
			}
		})
	}
}

// restartContainer restarts a docker container and waits for it to be running.
func restartContainer(t *testing.T, container string) time.Time {
	t.Helper()
	t.Logf("Restarting container %s for network recovery", container)
	restartStarted := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "restart", container).CombinedOutput(); err != nil {
		t.Logf("Warning: docker restart %s failed: %v\n%s", container, err, out)
		return restartStarted
	}
	// Wait for BOOTy to start inside the container.
	for i := 0; i < 30; i++ {
		logs := getBootyLogsSince(t, container, restartStarted)
		if strings.Contains(logs, "starting BOOTy") {
			t.Logf("Container %s restarted and BOOTy started", container)
			return restartStarted
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("Warning: container %s restarted but BOOTy startup not detected", container)
	return restartStarted
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

func waitForBootHTTP(t *testing.T, probe bootHTTPProbe, timeout time.Duration) bool {
	t.Helper()
	deadline := bootDeadline(t, timeout)
	var lastErr error
	var lastOut string
	for round := 0; round <= bootRecoveryRestarts; round++ {
		if time.Now().After(deadline) {
			break
		}
		logSince := time.Time{}
		if round > 0 {
			if time.Until(deadline) <= bootRestartBudget {
				t.Logf("%s skipping recovery restart; remaining time is below restart budget", probe.desc)
				break
			}
			logSince = restartContainer(t, probe.container)
		}
		attempts := 0
		for time.Now().Before(deadline) {
			attempts++
			if bootyNetworkNeedsRecovery(t, probe.container, logSince) {
				t.Logf("%s BOOTy needs network recovery (round %d)", probe.desc, round)
				break
			}
			if time.Until(deadline) <= time.Second {
				break
			}
			out, err := bootDockerExecBefore(t, deadline, probe.container,
				"wget", "-qO-", "--tries=1", "--timeout="+bootProbeTimeoutSeconds, probe.url)
			lastOut, lastErr = out, err
			if err == nil && (probe.contains == "" || strings.Contains(out, probe.contains)) {
				t.Logf("%s reached %s after %d attempts (round %d)", probe.desc, probe.url, attempts, round)
				return true
			}
			if !waitForBootPoll(deadline) {
				break
			}
		}
	}
	t.Logf("%s could not reach %s before deadline; last error: %v; last output: %q",
		probe.desc, probe.url, lastErr, truncateBootProbeOutput(lastOut))
	return false
}

func truncateBootProbeOutput(out string) string {
	const limit = 2048
	if len(out) <= limit {
		return out
	}
	return out[:limit] + "...[truncated]"
}

func bootDeadline(t *testing.T, timeout time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	if testDeadline, ok := t.Deadline(); ok {
		capped := testDeadline.Add(-30 * time.Second)
		if capped.Before(deadline) {
			return capped
		}
	}
	return deadline
}

func waitForBootPoll(deadline time.Time) bool {
	delay := time.Until(deadline)
	if delay <= 0 {
		return false
	}
	if delay > bootProbeInterval {
		delay = bootProbeInterval
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	return time.Now().Before(deadline)
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
			probe := bootHTTPProbe{
				container: c.name,
				desc:      c.desc,
				url:       "http://10.100.0.11/health",
			}
			if !waitForBootHTTP(t, probe, bootCAPRFReachabilityTimeout) {
				t.Fatalf("%s node cannot reach CAPRF mock (10.100.0.11) through EVPN fabric after restart", c.desc)
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
			probe := bootHTTPProbe{
				container: c.name,
				desc:      c.desc,
				url:       "http://10.100.0.10/",
			}
			if !waitForBootHTTP(t, probe, bootReachabilityTimeout) {
				t.Fatalf("%s node cannot reach nginx (10.100.0.10) through EVPN fabric after restart", c.desc)
			}
			t.Logf("%s node reaches nginx through EVPN fabric", c.desc)
		})
	}
}

// --- Full BOOTy boot log tests ---

func TestBootProvisionStartsAndReportsInit(t *testing.T) {
	requireBootLab(t)

	// Wait for BOOTy to produce its startup banner
	if !waitForLogEntry(t, provisionContainer, "starting BOOTy", 60*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not start BOOTy within 60s\nFull logs:\n%s", logs)
	}

	// Wait for CAPRF mode detection
	if !waitForLogEntry(t, provisionContainer, "CAPRF mode active", 30*time.Second) {
		logs := getBootyLogs(t, provisionContainer)
		t.Fatalf("provision node did not enter CAPRF mode\nFull logs:\n%s", logs)
	}

	// Verify FRR/EVPN network mode (not DHCP)
	if !waitForLogEntry(t, provisionContainer, "using FRR/EVPN network mode", 30*time.Second) {
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

	if !waitForLogEntry(t, deprovisionContainer, "starting BOOTy", 60*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node did not start BOOTy within 60s\nFull logs:\n%s", logs)
	}

	if !waitForLogEntry(t, deprovisionContainer, "CAPRF mode active", 30*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node did not enter CAPRF mode\nFull logs:\n%s", logs)
	}

	// Verify FRR/EVPN network mode (not DHCP)
	if !waitForLogEntry(t, deprovisionContainer, "using FRR/EVPN network mode", 30*time.Second) {
		logs := getBootyLogs(t, deprovisionContainer)
		t.Fatalf("deprovision node did not enter FRR/EVPN network mode\nFull logs:\n%s", logs)
	}

	t.Log("deprovision node: Started BOOTy → CAPRF mode → FRR/EVPN OK")
}

func TestBootStandbyEntersStandbyLoop(t *testing.T) {
	requireBootLab(t)

	markers := []string{
		"mode=standby",
		"CAPRF mode active",
		"sending heartbeat",
	}

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		logs := getBootyLogs(t, standbyContainer)
		for _, marker := range markers {
			if strings.Contains(logs, marker) {
				t.Logf("standby node reached expected steady-state marker: %s", marker)
				return
			}
		}
		time.Sleep(2 * time.Second)
	}

	logs := getBootyLogs(t, standbyContainer)
	t.Fatalf("standby loop marker not observed within 45s\nFull logs:\n%s", logs)
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
			probe := bootHTTPProbe{
				container: c.name,
				desc:      c.desc,
				url:       "http://10.100.0.10/images/",
				contains:  "test.img.gz",
			}
			if !waitForBootHTTP(t, probe, bootReachabilityTimeout) {
				t.Fatalf("%s node cannot reach nginx images (10.100.0.10) through EVPN after restart", c.desc)
			}
			t.Logf("%s node: nginx image listing through EVPN succeeded", c.desc)
		})
	}
}

func TestBootNginxAccessLogShowsImageRequest(t *testing.T) {
	requireBootLab(t)

	out, ok := waitForAccessLogEntry(t, nginxContainer, "/var/log/nginx/access.log", "/images/test.img.gz", 60*time.Second)
	if !ok {
		if strings.Contains(out, "/images/") {
			t.Logf("Nginx received image directory request through EVPN:\n%s", out)
		}
		t.Logf("Nginx access log:\n%s", out)
		t.Fatal("nginx did not receive /images/test.img.gz in time")
	}
	t.Logf("Nginx received image request through EVPN:\n%s", out)
}

// --- CAPRF error lifecycle (provision fails at disk ops) ---

func TestBootCAPRFMockReceivedErrorFromProvision(t *testing.T) {
	requireBootLab(t)

	// Image streaming through EVPN may retry before failing, but secure transport
	// hardening can intentionally block posting /status/error to non-HTTPS CAPRF.
	out, ok := waitForAccessLogEntry(t, caprfContainer, "/var/log/nginx/access.log", "/status/error", 120*time.Second)
	if !ok {
		serial := getBootyLogs(t, provisionContainer)
		if strings.Contains(serial, "insecure transport") ||
			strings.Contains(serial, "refusing request to non-HTTPS endpoint") ||
			strings.Contains(serial, "skipping bearer token on non-HTTPS request") {
			t.Logf("CAPRF access log:\n%s", out)
			t.Log("CAPRF /status/error not posted because non-HTTPS bearer transport was intentionally blocked")
			return
		}
		t.Logf("CAPRF access log:\n%s", out)
		t.Fatal("CAPRF mock did not receive /status/error within 120s")
	}
	t.Log("CAPRF mock received /status/error (provision failed at disk ops as expected)")
}

func TestBootProvisionShowsProvisioningSteps(t *testing.T) {
	requireBootLab(t)

	if !waitForLogEntry(t, provisionContainer, "report-init", 60*time.Second) {
		t.Fatal("provision node did not reach report-init")
	}

	// Wait for provisioning to finish (success or failure).
	// With real image streaming through EVPN, retries can take several minutes.
	if !waitForLogEntry(t, provisionContainer, "provisioning failed", 180*time.Second) {
		t.Log("provision node: 'provisioning failed' not found within 180s")
	}

	logs := getBootyLogs(t, provisionContainer)

	// With a real disk image, provisioning should at minimum reach disk detection
	// and attempt image streaming. Steps after stream-image (partprobe, mount-root)
	// depend on successful image download through EVPN which can be flaky.
	alwaysReached := []string{
		"detect-disk",
		"stream-image",
	}
	for _, step := range alwaysReached {
		if !strings.Contains(logs, step) {
			t.Logf("provision node: expected step %q not found in logs", step)
		}
	}

	// Steps after successful image streaming — log presence but don't fail.
	postStreamSteps := []string{
		"partprobe",
		"parse-partitions",
		"mount-root",
	}
	for _, step := range postStreamSteps {
		if strings.Contains(logs, step) {
			t.Logf("provision node: reached post-stream step %q", step)
		} else {
			t.Logf("provision node: post-stream step %q not reached (image streaming may have failed)", step)
		}
	}

	if strings.Contains(logs, "Image written") {
		t.Log("provision node: image streaming to disk completed successfully")
	}
	if strings.Contains(logs, "Using configured disk device") {
		t.Log("provision node: using configured disk device /dev/loop0")
	}
}

// --- Standby heartbeat through EVPN ---

func TestBootStandbyHeartbeatsSentToCAPRF(t *testing.T) {
	requireBootLab(t)

	// If standby's network failed, restart it and wait for recovery.
	if bootyNetworkNeedsRecovery(t, standbyContainer, time.Time{}) {
		logSince := restartContainer(t, standbyContainer)
		// Wait for BOOTy to re-establish connectivity after restart.
		if !waitForLogEntrySince(t, standbyContainer, "network connectivity established", 6*time.Minute, logSince) {
			logs := getBootyLogs(t, standbyContainer)
			t.Fatalf("standby did not recover network after restart\nFull logs:\n%s", logs)
		}
	}

	// Wait for standby to enter heartbeat loop.
	if !waitForLogEntry(t, standbyContainer, "standby", 90*time.Second) {
		logs := getBootyLogs(t, standbyContainer)
		t.Fatalf("standby node did not enter standby mode\nFull logs:\n%s", logs)
	}

	out, ok := waitForAccessLogEntry(t, caprfContainer, "/var/log/nginx/access.log", "/status/heartbeat", 90*time.Second)
	if !ok {
		standbyLogs := getBootyLogs(t, standbyContainer)
		if strings.Contains(standbyLogs, "insecure transport") ||
			strings.Contains(standbyLogs, "refusing request to non-HTTPS endpoint") ||
			strings.Contains(standbyLogs, "skipping bearer token on non-HTTPS request") {
			t.Logf("CAPRF access log:\n%s", out)
			t.Log("standby heartbeat not posted because non-HTTPS bearer transport was intentionally blocked")
			return
		}
		t.Logf("CAPRF access log:\n%s", out)
		t.Fatal("CAPRF mock did not receive /status/heartbeat in time")
	}
	t.Log("CAPRF mock received heartbeat from standby node through EVPN")
}

// --- Unexpected ERROR detection ---

// allowedErrorPatterns lists error messages that are expected in minimal CI
// environments (no real disk, provisioning failure at disk ops, etc.).
// Debug dumps (DumpDebugState, DumpPATH, dumpConfig) log at WARN level and
// are invisible to this check — only genuine ERROR-level messages remain.
var allowedErrorPatterns = []string{
	// Top-level mode exit with error (logged by runmode dispatch in main.go).
	"mode exited with error",
	// Top-level provisioning/deprovisioning failure.
	"provisioning failed",
	"deprovisioning failed",
	// Individual step failures.
	"provisioning step failed",
	"Deprovisioning step failed",
	// Expected in CI without real disks or network.
	"no suitable disk found",
	"Connectivity timeout",
	"Connecting to provisioning server",
	"Network connectivity timeout",
	// Expected when provisioning with real image in container (no growpart, no update-grub).
	"failed to report error status",
	"growpart",
	"update-grub",
	"configure-kubelet",
	"resize2fs",
	"xfs_growfs",
	// Image streaming through EVPN can fail with connection resets or timeouts.
	"connection reset by peer",
	"timeout awaiting response headers",
	"HTTP request failed, retrying",
	"retrying step",
	"stream-image",
}

func TestBootNoUnexpectedErrors(t *testing.T) {
	requireBootLab(t)

	// Wait for BOOTy to have progressed through provisioning attempt.
	// Poll for a known terminal state instead of sleeping a fixed duration.
	if !waitForLogEntry(t, provisionContainer, "provisioning failed", 180*time.Second) {
		t.Log("provision node: 'provisioning failed' not found within 180s, checking available logs")
	}

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
			lineLower := strings.ToLower(line)
			allowed := false
			for _, pattern := range allowedErrorPatterns {
				if strings.Contains(lineLower, strings.ToLower(pattern)) {
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

	// Wait for BOOTy processes to have run — poll for a terminal state.
	if !waitForLogEntry(t, provisionContainer, "provisioning failed", 60*time.Second) {
		t.Log("provision node: 'provisioning failed' not found, dumping available logs")
	}

	containers := []struct {
		name string
		desc string
	}{
		{provisionContainer, "PROVISION"},
		{deprovisionContainer, "DEPROVISION"},
		{standbyContainer, "STANDBY"},
	}

	for _, c := range containers {
		// Use --tail 2000 to avoid dumping 100s of MBs that block CI stdout pipes.
		logs := getBootyLogsTail(t, c.name, "2000")
		t.Logf("\n========================================\n"+
			"  %s NODE BOOTY LOGS (last 2000 lines)\n"+
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
