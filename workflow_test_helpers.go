package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowFile struct {
	Jobs map[string]*workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Name            string         `yaml:"name"`
	Needs           any            `yaml:"needs"`
	If              string         `yaml:"if"`
	ContinueOnError any            `yaml:"continue-on-error"`
	TimeoutMinutes  int            `yaml:"timeout-minutes"`
	Steps           []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
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

func requireWorkflowStep(t *testing.T, job *workflowJob, name string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing step %q", name)
	return workflowStep{}
}

func requireStep(t *testing.T, job *workflowJob, name string) workflowStep {
	t.Helper()
	return requireWorkflowStep(t, job, name)
}

func requireWorkflowStepRunContains(
	t *testing.T,
	job *workflowJob,
	name string,
	want string,
) workflowStep {
	t.Helper()
	step := requireWorkflowStep(t, job, name)
	if !strings.Contains(step.Run, want) {
		t.Fatalf("%s run = %q, want to contain %q", name, step.Run, want)
	}
	return step
}

func requireWorkflowAction(t *testing.T, job *workflowJob, action string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if actionMatches(step.Uses, action) {
			return step
		}
	}
	t.Fatalf("missing action %s", action)
	return workflowStep{}
}

func requireAction(t *testing.T, job *workflowJob, action string) workflowStep {
	t.Helper()
	return requireWorkflowAction(t, job, action)
}

func assertNoAction(t *testing.T, job *workflowJob, action string) {
	t.Helper()
	for _, step := range job.Steps {
		if actionMatches(step.Uses, action) {
			t.Fatalf("job must not use %s", action)
		}
	}
}

func assertWorkflowActionPinned(t *testing.T, step workflowStep, action string) {
	t.Helper()
	prefix := action + "@"
	if !strings.HasPrefix(step.Uses, prefix) {
		t.Fatalf("%s uses = %q, want %s<sha>", step.Name, step.Uses, prefix)
	}
	ref := strings.TrimPrefix(step.Uses, prefix)
	if len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "" {
		t.Fatalf("%s uses mutable ref %q, want 40-character SHA", step.Name, ref)
	}
}

func assertWorkflowWith(t *testing.T, step workflowStep, key, want string) {
	t.Helper()
	if got := workflowValueString(step.With[key]); got != want {
		t.Fatalf("%s.%s = %q, want %q", step.Name, key, got, want)
	}
}

func assertStepWith(t *testing.T, step workflowStep, key, want string) {
	t.Helper()
	assertWorkflowWith(t, step, key, want)
}

func assertStepWithContains(t *testing.T, step workflowStep, key, want string) {
	t.Helper()
	if got := workflowValueString(step.With[key]); !strings.Contains(got, want) {
		t.Fatalf("%s.%s = %q, want to contain %q", step.Name, key, got, want)
	}
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
