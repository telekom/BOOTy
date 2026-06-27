package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type ciWorkflowFile struct {
	Jobs map[string]ciWorkflowJob `yaml:"jobs"`
}

type ciWorkflowJob struct {
	Name            string           `yaml:"name"`
	Needs           any              `yaml:"needs"`
	If              string           `yaml:"if"`
	ContinueOnError any              `yaml:"continue-on-error"`
	Steps           []ciWorkflowStep `yaml:"steps"`
}

type ciWorkflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
}

func TestCIWorkflowRunsProductionE2E(t *testing.T) {
	wf := loadCIWorkflow(t)
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
	if got := ciNeedsSet(job.Needs); !ciHasNeeds(got, "build") {
		t.Fatalf("e2e-production needs = %v, want [build]", got)
	}

	requireCIStepRunContains(t, job, "Deploy production topology", "topology-production.clab.yml")
	run := requireCIStep(t, job, "Run production E2E tests").Run
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
		if strings.Contains(run, unsupported+" \\") {
			t.Fatalf("Run production E2E tests includes unproven production assertion %q", unsupported)
		}
	}
	requireCIStepRunContains(t, job, "Cleanup", "topology-production.clab.yml")

	upload := requireCIAction(t, job, "actions/upload-artifact")
	if got := ciWorkflowValueString(upload.With["name"]); got != "production-e2e-logs" {
		t.Fatalf("upload artifact name = %q, want production-e2e-logs", got)
	}
}

func loadCIWorkflow(t *testing.T) ciWorkflowFile {
	t.Helper()
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}
	var wf ciWorkflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse ci workflow: %v", err)
	}
	return wf
}

func requireCIStep(t *testing.T, job ciWorkflowJob, name string) ciWorkflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing step %q", name)
	return ciWorkflowStep{}
}

func requireCIStepRunContains(t *testing.T, job ciWorkflowJob, name, want string) {
	t.Helper()
	run := requireCIStep(t, job, name).Run
	if !strings.Contains(run, want) {
		t.Fatalf("%s run = %q, want to contain %q", name, run, want)
	}
}

func requireCIAction(t *testing.T, job ciWorkflowJob, action string) ciWorkflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, action+"@") {
			return step
		}
	}
	t.Fatalf("missing action %s", action)
	return ciWorkflowStep{}
}

func ciHasNeeds(got map[string]bool, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, need := range want {
		if !got[need] {
			return false
		}
	}
	return true
}

func ciNeedsSet(value any) map[string]bool {
	out := map[string]bool{}
	switch v := value.(type) {
	case string:
		out[v] = true
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

func ciWorkflowValueString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
