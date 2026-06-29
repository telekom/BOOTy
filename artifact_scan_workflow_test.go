package main

import (
	"os"
	"strings"
	"testing"
)

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
			job := loadWorkflow(t, tt.path).Jobs["scan"]
			download := requireWorkflowAction(t, job, "actions/download-artifact")
			assertWorkflowActionPinned(t, download, "actions/download-artifact")
			assertWorkflowWith(t, download, "pattern", tt.pattern)
			assertWorkflowWith(t, download, "path", tt.artifact)

			requireWorkflowStepRunContains(t, job, "Extract initramfs artifacts", tt.artifact)
			requireWorkflowStepRunContains(t, job, "Extract initramfs artifacts", "scan-rootfs")

			trivy := requireWorkflowAction(t, job, "aquasecurity/trivy-action")
			assertWorkflowActionPinned(t, trivy, "aquasecurity/trivy-action")
			assertWorkflowWith(t, trivy, "scan-type", "fs")
			assertWorkflowWith(t, trivy, "scan-ref", "scan-rootfs")
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
	job := loadWorkflow(t, ".github/workflows/release-v2.yml").Jobs["sbom"]
	step := requireWorkflowStepRunContains(t, job, "Generate SBOM checksum", "sbom.spdx.json.sha256")
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
