package main

import "testing"

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
