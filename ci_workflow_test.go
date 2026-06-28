package main

import (
	"strings"
	"testing"
)

func TestCIWorkflowRunsProductionE2E(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/ci.yml")
	job, ok := wf.Jobs["e2e-production"]
	if !ok {
		t.Fatal("missing e2e-production job")
	}
	if job.Name != "E2E Production Smoke (ContainerLab)" {
		t.Fatalf("e2e-production name = %q", job.Name)
	}
	if job.If != "" {
		t.Fatalf("e2e-production has job-level if %q", job.If)
	}
	if job.ContinueOnError != nil {
		t.Fatalf("e2e-production continue-on-error = %v, want unset", job.ContinueOnError)
	}
	if got := needsSet(job.Needs); !hasNeeds(got, "build") {
		t.Fatalf("e2e-production needs = %v, want [build]", got)
	}
	if job.TimeoutMinutes != 30 {
		t.Fatalf("e2e-production timeout-minutes = %d, want 30", job.TimeoutMinutes)
	}
	checkout := requireWorkflowAction(t, job, "actions/checkout")
	assertWorkflowActionPinned(t, checkout, "actions/checkout")
	if got, ok := checkout.With["persist-credentials"].(bool); !ok || got {
		t.Fatalf("checkout persist-credentials = %v, want false", checkout.With["persist-credentials"])
	}
	setupGo := requireWorkflowAction(t, job, "actions/setup-go")
	assertWorkflowActionPinned(t, setupGo, "actions/setup-go")

	requireWorkflowStepRunContains(t, job, "Deploy production topology", "topology-production.clab.yml")
	run := requireWorkflowStep(t, job, "Run production E2E tests").Run
	for _, want := range []string{
		"make test-e2e-production",
		"production-e2e.log",
		"TestProductionGatewayRoute",
		"TestProductionOverlayReachCAPRF",
		"--- SKIP:",
	} {
		if !strings.Contains(run, want) {
			t.Fatalf("Run production E2E tests missing %q", want)
		}
	}
	for _, unsupported := range []string{
		"TestProductionVRFCreated",
		"TestProductionBFDActiveOnDCGW",
		"TestProductionEVPNType5OnSpine",
		"TestProductionEVPNType5OnDCGW",
	} {
		if strings.Contains(run, unsupported) {
			t.Fatalf("Run production E2E tests includes unproven production assertion %q", unsupported)
		}
	}
	requireWorkflowStepRunContains(t, job, "Cleanup", "topology-production.clab.yml")

	upload := requireWorkflowAction(t, job, "actions/upload-artifact")
	assertWorkflowActionPinned(t, upload, "actions/upload-artifact")
	if got := workflowValueString(upload.With["name"]); got != "production-e2e-logs" {
		t.Fatalf("upload artifact name = %q, want production-e2e-logs", got)
	}
}
