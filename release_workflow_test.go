package main

import "testing"

func TestReleaseWorkflowRetriesGHCRAuthorizationPropagation(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/release-v2.yml")

	container, ok := wf.Jobs["container"]
	if !ok {
		t.Fatal("missing container job")
	}
	requireWorkflowStepRunContains(t, container, "Build and push per-arch image", "ensure_container_ref_absent")
	requireWorkflowStepRunContains(t, container, "Build and push per-arch image", "for attempt in 1 2 3 4 5")

	manifest, ok := wf.Jobs["container-manifest"]
	if !ok {
		t.Fatal("missing container-manifest job")
	}
	requireWorkflowStepRunContains(t, manifest, "Create and push multi-arch manifest", "ensure_container_ref_absent")
	requireWorkflowStepRunContains(t, manifest, "Create and push multi-arch manifest", `retry_ghcr "create container manifest`)
	requireWorkflowStepRunContains(t, manifest, "Sign container image", `retry_ghcr "sign ${IMAGE}"`)

	oci, ok := wf.Jobs["oci-artifacts"]
	if !ok {
		t.Fatal("missing oci-artifacts job")
	}
	requireWorkflowStepRunContains(t, oci, "Publish initramfs artifacts", "ensure_oci_ref_absent")
	requireWorkflowStepRunContains(t, oci, "Publish initramfs artifacts", `retry_ghcr "sign OCI artifact`)
	requireWorkflowStepRunContains(t, oci, "Publish binary artifact", `retry_ghcr "sign OCI artifact`)
	requireWorkflowStepRunContains(t, oci, "Publish SBOM artifact", `retry_ghcr "sign OCI artifact`)
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

func TestReleaseWorkflowRetriesTransientSBOMDownload(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/release-v2.yml")
	job := wf.Jobs["sbom"]
	if job == nil {
		t.Fatal("missing sbom job")
	}

	for _, name := range []string{
		"Generate SBOM (attempt 1)",
		"Generate SBOM (attempt 2)",
		"Generate SBOM (attempt 3)",
	} {
		step := requireWorkflowStep(t, job, name)
		assertWorkflowActionPinned(t, step, "anchore/sbom-action")
		assertStepWith(t, step, "path", "release-artifacts")
		assertStepWith(t, step, "upload-artifact", "false")
	}

	first := requireWorkflowStep(t, job, "Generate SBOM (attempt 1)")
	if first.ContinueOnError != true {
		t.Fatalf("first SBOM attempt continue-on-error = %v, want true", first.ContinueOnError)
	}
	second := requireWorkflowStep(t, job, "Generate SBOM (attempt 2)")
	if second.If != "steps.sbom-attempt-1.outcome == 'failure'" {
		t.Fatalf("second SBOM attempt condition = %q", second.If)
	}
}
