package main

import (
	"os"
	"strings"
	"testing"
)

func TestNightlyWorkflowRunsSmallTopologySmokeMatrix(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/nightly.yml")
	if err != nil {
		t.Fatalf("read nightly workflow: %v", err)
	}
	text := string(data)
	wf := loadWorkflow(t, ".github/workflows/nightly.yml")
	job, ok := wf.Jobs["topology-smoke"]
	if !ok {
		t.Fatal("missing topology-smoke job")
	}
	if got := needsSet(job.Needs); !hasNeeds(got, "build") {
		t.Fatalf("topology-smoke needs = %v, want build", got)
	}
	if job.TimeoutMinutes != 35 {
		t.Fatalf("topology-smoke timeout-minutes = %d, want 35", job.TimeoutMinutes)
	}
	requireWorkflowStepRunContains(t, job, "Deploy topology", "make clab-${{ matrix.make_target }}-up")
	requireWorkflowStepRunContains(t, job, "Run topology smoke test", "set -euo pipefail")
	requireWorkflowStepRunContains(t, job, "Run topology smoke test", "make test-e2e-${{ matrix.make_target }}")
	requireWorkflowStepRunContains(t, job, "Collect topology logs", "awk '/^clab-booty-/ { print }'")
	requireWorkflowStepRunContains(t, job, "Cleanup", "make clab-${{ matrix.make_target }}-down")

	for _, want := range []string{
		"topology: dhcp",
		"topology: static",
		"topology: multi-nic",
		"topology: bond",
		"name: topology-${{ matrix.topology }}-logs",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("nightly workflow missing topology matrix marker %q", want)
		}
	}

	container, ok := wf.Jobs["container"]
	if !ok {
		t.Fatal("missing container job")
	}
	if got := needsSet(container.Needs); !hasNeeds(got, "version", "build", "scan", "sbom", "topology-smoke") {
		t.Fatalf("container needs = %v, want topology-smoke included", got)
	}
}
