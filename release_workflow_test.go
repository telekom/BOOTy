package main

import (
	"os"
	"reflect"
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
	if got := needsList(job.Needs); !reflect.DeepEqual(got, []string{"build", "iso"}) {
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

func actionMatches(uses, action string) bool {
	return strings.HasPrefix(uses, action+"@")
}

func needsList(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
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
