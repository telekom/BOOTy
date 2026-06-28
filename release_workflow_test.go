package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowFile struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Needs any            `yaml:"needs"`
	Steps []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
}

func TestReleaseWorkflowSBOMScansReleaseArtifacts(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/release-v2.yml")
	job, ok := wf.Jobs["sbom"]
	if !ok {
		t.Fatal("missing sbom job")
	}
	if got := needsSet(job.Needs); !hasNeeds(got, "build", "iso") {
		t.Fatalf("sbom needs = %v, want [build iso]", got)
	}

	assertNoAction(t, job, "actions/checkout")
	download := requireAction(t, job, "actions/download-artifact")
	assertStepWith(t, download, "pattern", "release-*")
	assertStepWith(t, download, "path", "release-artifacts")
	assertStepWith(t, download, "merge-multiple", "false")

	sbom := requireAction(t, job, "anchore/sbom-action")
	assertStepWith(t, sbom, "path", "release-artifacts")
	assertStepWith(t, sbom, "upload-artifact", "false")
	assertStepWith(t, sbom, "upload-release-assets", "false")

	requireStep(t, job, "Generate SBOM checksum")
	upload := requireAction(t, job, "actions/upload-artifact")
	assertStepWith(t, upload, "name", "sbom")
	assertStepWithContains(t, upload, "path", "sbom.spdx.json")
	assertStepWithContains(t, upload, "path", "sbom.spdx.json.sha256")
	assertStepWith(t, upload, "if-no-files-found", "error")
}

func loadWorkflow(t *testing.T, path string) workflowFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return wf
}

func assertNoAction(t *testing.T, job workflowJob, action string) {
	t.Helper()
	for _, step := range job.Steps {
		if actionMatches(step.Uses, action) {
			t.Fatalf("sbom job must not use %s", action)
		}
	}
}

func requireAction(t *testing.T, job workflowJob, action string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if actionMatches(step.Uses, action) {
			return step
		}
	}
	t.Fatalf("missing action %s", action)
	return workflowStep{}
}

func assertStepWith(t *testing.T, step workflowStep, key, want string) {
	t.Helper()
	if got := workflowValueString(step.With[key]); got != want {
		t.Fatalf("%s.%s = %q, want %q", step.Name, key, got, want)
	}
}

func assertStepWithContains(t *testing.T, step workflowStep, key, want string) {
	t.Helper()
	if got := workflowValueString(step.With[key]); !strings.Contains(got, want) {
		t.Fatalf("%s.%s = %q, want to contain %q", step.Name, key, got, want)
	}
}

func requireStep(t *testing.T, job workflowJob, name string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing step %q", name)
	return workflowStep{}
}

func actionMatches(uses, action string) bool {
	return strings.HasPrefix(uses, action+"@")
}

func hasNeeds(got map[string]bool, want ...string) bool {
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

func needsSet(value any) map[string]bool {
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

func workflowValueString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}
