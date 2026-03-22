//go:build e2e_integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Container name constants for each topology.
const (
	dhcpServer   = "clab-booty-dhcp-lab-dhcp-server"
	dhcpBooty    = "clab-booty-dhcp-lab-booty"
	bondSwitch   = "clab-booty-bond-lab-switch"
	bondBooty    = "clab-booty-bond-lab-booty"
	staticRouter = "clab-booty-static-lab-router"
	staticBooty  = "clab-booty-static-lab-booty"
	multiSwitch  = "clab-booty-multi-nic-lab-switch"
	multiBooty   = "clab-booty-multi-nic-lab-booty"
	labSpine     = "clab-booty-lab-spine01"
	labLeaf      = "clab-booty-lab-leaf01"
	labCaprf     = "clab-booty-lab-caprf-mock"
)

type topologyCheck struct {
	container     string
	args          []string
	expectContain string // if non-empty, output must contain this substring
}

type topologySpec struct {
	containers []string
	checks     []topologyCheck
}

var topologySpecs = map[string]topologySpec{
	"lab": {
		containers: []string{labSpine, labLeaf, labCaprf},
		checks: []topologyCheck{
			{container: labSpine, args: []string{"vtysh", "-c", "show bgp summary"}, expectContain: "Estab"},
			{container: labSpine, args: []string{"vtysh", "-c", "show ip route"}, expectContain: "known"},
		},
	},
	"dhcp": {
		containers: []string{dhcpServer, dhcpBooty},
		checks: []topologyCheck{
			{container: dhcpServer, args: []string{"pgrep", "dhcpd"}},
		},
	},
	"bond": {
		containers: []string{bondSwitch, bondBooty},
		checks: []topologyCheck{
			{container: bondSwitch, args: []string{"ip", "link", "show", "br-bond"}},
		},
	},
	"static": {
		containers: []string{staticRouter, staticBooty},
		checks: []topologyCheck{
			{container: staticBooty, args: []string{"ip", "route", "show", "default"}},
		},
	},
	"multi-nic": {
		containers: []string{multiSwitch, multiBooty},
		checks: []topologyCheck{
			{container: multiSwitch, args: []string{"ip", "link", "show", "br-data"}},
			{container: multiBooty, args: []string{"ip", "addr", "show", "eth4"}},
		},
	},
}

func TestContainerLabTopologySmoke(t *testing.T) {
	topo := strings.TrimSpace(os.Getenv("BOOTY_TOPOLOGY"))
	if topo == "" {
		t.Fatal("BOOTY_TOPOLOGY not set")
	}

	spec, ok := topologySpecs[topo]
	if !ok {
		t.Fatalf("unsupported BOOTY_TOPOLOGY %q", topo)
	}

	out, err := exec.CommandContext(context.Background(), "docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker not available: %v", err)
	}
	running := strings.Split(strings.TrimSpace(string(out)), "\n")
	runningSet := make(map[string]bool, len(running))
	for _, name := range running {
		runningSet[name] = true
	}
	for _, name := range spec.containers {
		if !runningSet[name] {
			t.Fatalf("expected container %s not found in docker ps", name)
		}
	}

	// Retry topology checks to allow services to settle after clab deploy.
	for _, check := range spec.checks {
		var cmdOut string
		var cmdErr error
		for attempt := 0; attempt < 5; attempt++ {
			cmdOut, cmdErr = dockerExecRaw(t, check.container, check.args...)
			if cmdErr == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if cmdErr != nil {
			t.Fatalf("topology check failed on %s after retries: %v\n%s", check.container, cmdErr, cmdOut)
		}
		if check.expectContain != "" && !strings.Contains(cmdOut, check.expectContain) {
			t.Fatalf("topology check on %s: output missing %q\n%s", check.container, check.expectContain, cmdOut)
		}
	}
}
