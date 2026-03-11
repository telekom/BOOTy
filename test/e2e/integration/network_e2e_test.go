//go:build e2e_integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// requireNetworkLab skips the test if the full network topology is not deployed.
func requireNetworkLab(t *testing.T) {
	t.Helper()
	requireContainerLab(t)
	out, _ := dockerExecRaw(t, "clab-booty-lab-client", "echo", "ok")
	if !strings.Contains(out, "ok") {
		t.Skip("client container not available in topology")
	}
}

// curlFromClient executes curl inside the client container with retry.
// It polls every 2s until the command succeeds or timeout is reached.
func curlFromClient(t *testing.T, url string, extraArgs ...string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	args := append([]string{"curl", "-sf", "--connect-timeout", "3"}, extraArgs...)
	args = append(args, url)
	for {
		out, err := dockerExecRaw(t, "clab-booty-lab-client", args...)
		if err == nil {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("curl %s failed after 30s: %v\n%s", url, err, out)
		}
		time.Sleep(2 * time.Second)
	}
}

// curlFromClientPost executes a POST curl from the client container with retry.
func curlFromClientPost(t *testing.T, url string) (int, string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := dockerExecRaw(t, "clab-booty-lab-client",
			"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
			"--connect-timeout", "3", "-X", "POST", url)
		if err == nil {
			code := strings.TrimSpace(out)
			return parseHTTPCode(code), code
		}
		if time.Now().After(deadline) {
			t.Fatalf("POST %s failed after 30s: %v\n%s", url, err, out)
		}
		time.Sleep(2 * time.Second)
	}
}

func parseHTTPCode(s string) int {
	code := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			code = code*10 + int(c-'0')
		}
	}
	return code
}

// dockerExecRaw runs docker exec and returns stdout+stderr and error.
func dockerExecRaw(t *testing.T, container string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

func TestVXLANBridgesCreated(t *testing.T) {
	requireNetworkLab(t)

	// Verify spine01 has br100 and vxlan100.
	out := dockerExec(t, "clab-booty-lab-spine01", "ip", "-d", "link", "show", "br100")
	if !strings.Contains(out, "br100") {
		t.Fatalf("br100 not found on spine01:\n%s", out)
	}
	out = dockerExec(t, "clab-booty-lab-spine01", "ip", "-d", "link", "show", "vxlan100")
	if !strings.Contains(out, "vxlan100") {
		t.Fatalf("vxlan100 not found on spine01:\n%s", out)
	}
	t.Log("spine01: br100 and vxlan100 present")
}

func TestClientPingNginxThroughEVPN(t *testing.T) {
	requireNetworkLab(t)

	// Wait for EVPN convergence, then ping nginx from client through VXLAN overlay.
	var reachable bool
	for range 30 {
		out, err := dockerExecRaw(t, "clab-booty-lab-client", "ping", "-c", "1", "-W", "1", "10.100.0.10")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			t.Log("nginx reachable from client through EVPN fabric")
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("nginx (10.100.0.10) not reachable from client after 30s")
	}
}

func TestClientPingCAPRFMockThroughEVPN(t *testing.T) {
	requireNetworkLab(t)

	var reachable bool
	for range 30 {
		out, err := dockerExecRaw(t, "clab-booty-lab-client", "ping", "-c", "1", "-W", "1", "10.100.0.11")
		if err == nil && strings.Contains(out, "1 packets received") {
			reachable = true
			t.Log("caprf-mock reachable from client through EVPN fabric")
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !reachable {
		t.Fatal("caprf-mock (10.100.0.11) not reachable from client after 30s")
	}
}

func TestNginxStaticContentThroughFabric(t *testing.T) {
	requireNetworkLab(t)

	body := curlFromClient(t, "http://10.100.0.10/")
	if !strings.Contains(body, "booty-lab") {
		t.Fatalf("expected 'booty-lab' in response, got:\n%s", body)
	}
	t.Log("nginx static content served through EVPN fabric")
}

func TestImageDownloadThroughFabric(t *testing.T) {
	requireNetworkLab(t)

	body := curlFromClient(t, "http://10.100.0.10/images/test.img")
	expected := "BOOTY-TEST-IMAGE-CONTENT-e2e-verification-payload"
	if !strings.Contains(body, expected) {
		t.Fatalf("image content mismatch:\nexpected: %s\ngot: %s", expected, body)
	}
	t.Log("test image downloaded through EVPN fabric")
}

func TestCAPRFStatusReportingThroughFabric(t *testing.T) {
	requireNetworkLab(t)

	code, raw := curlFromClientPost(t, "http://10.100.0.11/status/init")
	if code != 200 {
		t.Fatalf("expected HTTP 200 for /status/init, got %d (%s)", code, raw)
	}
	t.Log("CAPRF status/init reporting works through EVPN fabric")
}

func TestCAPRFHeartbeatThroughFabric(t *testing.T) {
	requireNetworkLab(t)

	code, raw := curlFromClientPost(t, "http://10.100.0.11/status/heartbeat")
	if code != 200 {
		t.Fatalf("expected HTTP 200 for /status/heartbeat, got %d (%s)", code, raw)
	}
	t.Log("CAPRF heartbeat works through EVPN fabric")
}

func TestCAPRFCommandsPollThroughFabric(t *testing.T) {
	requireNetworkLab(t)

	// GET /commands should return 204 No Content (no pending commands).
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := dockerExecRaw(t, "clab-booty-lab-client",
			"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
			"--connect-timeout", "3", "http://10.100.0.11/commands")
		if err == nil {
			code := parseHTTPCode(strings.TrimSpace(out))
			if code == 204 {
				t.Log("CAPRF /commands returns 204 through EVPN fabric")
				return
			}
			t.Fatalf("expected HTTP 204 for /commands, got %d", code)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /commands failed after 30s: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}
