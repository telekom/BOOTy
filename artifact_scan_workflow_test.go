package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type artifactScanWorkflowFile struct {
	Jobs map[string]artifactScanWorkflowJob `yaml:"jobs"`
}

type artifactScanWorkflowJob struct {
	Steps []artifactScanWorkflowStep `yaml:"steps"`
}

type artifactScanWorkflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
}

func TestReleaseAndNightlyScanBuiltInitramfsArtifacts(t *testing.T) {
	for _, tt := range []struct {
		name     string
		path     string
		pattern  string
		artifact string
	}{
		{
			name:     "release",
			path:     ".github/workflows/release-v2.yml",
			pattern:  "release-*",
			artifact: "release-artifacts",
		},
		{
			name:     "nightly",
			path:     ".github/workflows/nightly.yml",
			pattern:  "nightly-*",
			artifact: "nightly-artifacts",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := loadArtifactScanWorkflow(t, tt.path).Jobs["scan"]
			download := requireArtifactScanAction(t, job, "actions/download-artifact")
			assertArtifactScanActionPinned(t, download)
			assertArtifactScanWith(t, download, "pattern", tt.pattern)
			assertArtifactScanWith(t, download, "path", tt.artifact)

			requireArtifactScanRun(t, job, "Extract initramfs artifacts", tt.artifact)
			requireArtifactScanRun(t, job, "Extract initramfs artifacts", "scan-rootfs")

			trivy := requireArtifactScanAction(t, job, "aquasecurity/trivy-action")
			assertArtifactScanActionPinned(t, trivy)
			assertArtifactScanWith(t, trivy, "scan-type", "fs")
			assertArtifactScanWith(t, trivy, "scan-ref", "scan-rootfs")
		})
	}
}

func TestInitramfsExtractionScriptFailsClosed(t *testing.T) {
	data, err := os.ReadFile("hack/extract-initramfs-artifacts.sh")
	if err != nil {
		t.Fatalf("read extraction script: %v", err)
	}
	script := string(data)
	for _, want := range []string{
		"validate_inputs",
		"refusing unsafe extraction destination",
		"rm -rf --",
		"validate_cpio_entries",
		"unsafe cpio entry",
		"../*",
		"/*",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("extraction script missing fail-closed guard %q", want)
		}
	}
}

func TestReleaseSBOMChecksumFailsClosed(t *testing.T) {
	job := loadArtifactScanWorkflow(t, ".github/workflows/release-v2.yml").Jobs["sbom"]
	step := requireArtifactScanRun(t, job, "Generate SBOM checksum", "sbom.spdx.json.sha256")
	for _, want := range []string{
		"sha256sum sbom.spdx.json > sbom.spdx.json.sha256",
		"[ ! -s sbom.spdx.json.sha256 ]",
		"SBOM checksum generation did not produce sbom.spdx.json.sha256",
	} {
		if !strings.Contains(step.Run, want) {
			t.Fatalf("%s run = %q, want to contain %q", step.Name, step.Run, want)
		}
	}
}

func loadArtifactScanWorkflow(t *testing.T, path string) artifactScanWorkflowFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf artifactScanWorkflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return wf
}

func requireArtifactScanAction(
	t *testing.T,
	job artifactScanWorkflowJob,
	action string,
) artifactScanWorkflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, action+"@") {
			return step
		}
	}
	t.Fatalf("missing action %s", action)
	return artifactScanWorkflowStep{}
}

func requireArtifactScanRun(
	t *testing.T,
	job artifactScanWorkflowJob,
	name string,
	want string,
) artifactScanWorkflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			if !strings.Contains(step.Run, want) {
				t.Fatalf("%s run = %q, want to contain %q", name, step.Run, want)
			}
			return step
		}
	}
	t.Fatalf("missing step %q", name)
	return artifactScanWorkflowStep{}
}

func assertArtifactScanWith(t *testing.T, step artifactScanWorkflowStep, key, want string) {
	t.Helper()
	if got := artifactScanWorkflowString(step.With[key]); got != want {
		t.Fatalf("%s.%s = %q, want %q", step.Name, key, got, want)
	}
}

func assertArtifactScanActionPinned(t *testing.T, step artifactScanWorkflowStep) {
	t.Helper()
	parts := strings.Split(step.Uses, "@")
	if len(parts) != 2 {
		t.Fatalf("%s uses = %q, want action@ref", step.Name, step.Uses)
	}
	ref := parts[1]
	if len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "" {
		t.Fatalf("%s uses mutable ref %q, want 40-character SHA", step.Name, ref)
	}
}

func artifactScanWorkflowString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
