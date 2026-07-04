package main

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseAndNightlyScanBuiltInitramfsArtifacts(t *testing.T) {
	for _, tt := range []struct {
		name         string
		path         string
		pattern      string
		artifactName string
		artifactPath string
		assertChown  bool
	}{
		{
			name:         "release",
			path:         ".github/workflows/release-v2.yml",
			pattern:      "release-*",
			artifactPath: "release-artifacts",
		},
		{
			name:         "nightly",
			path:         ".github/workflows/nightly.yml",
			artifactName: "nightly-default-amd64",
			artifactPath: "nightly-artifacts",
			assertChown:  true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := loadWorkflow(t, tt.path).Jobs["scan"]
			download := requireWorkflowAction(t, job, "actions/download-artifact")
			assertWorkflowActionPinned(t, download, "actions/download-artifact")
			if tt.name == "nightly" && download.Name != "Download nightly artifact" {
				t.Fatalf("download step name = %q, want Download nightly artifact", download.Name)
			}
			if tt.pattern != "" {
				assertWorkflowWith(t, download, "pattern", tt.pattern)
				assertWorkflowWith(t, download, "merge-multiple", "false")
			}
			if tt.artifactName != "" {
				assertWorkflowWith(t, download, "name", tt.artifactName)
			}
			assertWorkflowWith(t, download, "path", tt.artifactPath)

			extractStepName := "Extract initramfs artifacts"
			if tt.name == "nightly" {
				extractStepName = "Extract initramfs artifact"
			}
			requireWorkflowStepRunContains(t, job, extractStepName, tt.artifactPath)
			requireWorkflowStepRunContains(t, job, extractStepName, "scan-rootfs")
			if tt.assertChown {
				requireWorkflowStepRunContains(t, job, extractStepName, "chown -hR")
			}

			trivy := requireWorkflowAction(t, job, "aquasecurity/trivy-action")
			assertWorkflowActionPinned(t, trivy, "aquasecurity/trivy-action")
			assertWorkflowWith(t, trivy, "scan-type", "fs")
			assertWorkflowWith(t, trivy, "scan-ref", "scan-rootfs")
		})
	}
}

func TestNightlySBOMUsesCanonicalInitramfsArtifact(t *testing.T) {
	job := loadWorkflow(t, ".github/workflows/nightly.yml").Jobs["sbom"]
	download := requireWorkflowAction(t, job, "actions/download-artifact")

	assertWorkflowActionPinned(t, download, "actions/download-artifact")
	if download.Name != "Download nightly artifact" {
		t.Fatalf("download step name = %q, want Download nightly artifact", download.Name)
	}
	assertWorkflowWith(t, download, "name", "nightly-default-amd64")
	assertWorkflowWith(t, download, "path", "nightly-artifacts")

	requireWorkflowStepRunContains(t, job, "Extract initramfs artifact", "nightly-artifacts")
	requireWorkflowStepRunContains(t, job, "Extract initramfs artifact", "sbom-rootfs")
	requireWorkflowStepRunContains(t, job, "Extract initramfs artifact", "chown -hR")

	sbom := requireWorkflowAction(t, job, "anchore/sbom-action")
	assertWorkflowActionPinned(t, sbom, "anchore/sbom-action")
	assertWorkflowWith(t, sbom, "path", "sbom-rootfs/initramfs")
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
