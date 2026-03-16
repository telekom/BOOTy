//go:build e2e_integration

package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

type topologyCheck struct {
	container string
	args      []string
}

type topologySpec struct {
	containers []string
	checks     []topologyCheck
}

var topologySpecs = map[string]topologySpec{
	"dhcp": {
		containers: []string{"clab-booty-dhcp-lab-dhcp-server", "clab-booty-dhcp-lab-booty"},
		checks: []topologyCheck{
			{container: "clab-booty-dhcp-lab-dhcp-server", args: []string{"pgrep", "dhcpd"}},
		},
	},
	"bond": {
		containers: []string{"clab-booty-bond-lab-switch", "clab-booty-bond-lab-booty"},
		checks: []topologyCheck{
			{container: "clab-booty-bond-lab-switch", args: []string{"ip", "link", "show", "br-bond"}},
		},
	},
	"static": {
		containers: []string{"clab-booty-static-lab-router", "clab-booty-static-lab-booty"},
		checks: []topologyCheck{
			{container: "clab-booty-static-lab-booty", args: []string{"ip", "route", "show", "default"}},
		},
	},
	"multi-nic": {
		containers: []string{"clab-booty-multi-nic-lab-switch", "clab-booty-multi-nic-lab-booty"},
		checks: []topologyCheck{
			{container: "clab-booty-multi-nic-lab-switch", args: []string{"ip", "link", "show", "br-data"}},
			{container: "clab-booty-multi-nic-lab-booty", args: []string{"ip", "addr", "show", "eth4"}},
		},
	},
}

func TestContainerLabTopologySmoke(t *testing.T) {
	topo := strings.TrimSpace(os.Getenv("BOOTY_TOPOLOGY"))
	if topo == "" {
		t.Skip("BOOTY_TOPOLOGY not set")
	}

	spec, ok := topologySpecs[topo]
	if !ok {
		t.Fatalf("unsupported BOOTY_TOPOLOGY %q", topo)
	}

	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	ps := string(out)
	for _, name := range spec.containers {
		if !strings.Contains(ps, name) {
			t.Fatalf("expected container %s not found in docker ps", name)
		}
	}

	for _, check := range spec.checks {
		cmdOut, cmdErr := dockerExecRaw(t, check.container, check.args...)
		if cmdErr != nil {
			t.Fatalf("topology check failed on %s: %v\n%s", check.container, cmdErr, cmdOut)
		}
	}
}
