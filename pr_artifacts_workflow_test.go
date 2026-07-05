package main

import (
	"os"
	"strings"
	"testing"
)

func TestPRArtifactWorkflowPublishesDefaultAndGoBGPISOs(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/pr-artifacts.yml")
	if err != nil {
		t.Fatalf("read pr artifact workflow: %v", err)
	}
	text := string(data)
	wf := loadWorkflow(t, ".github/workflows/pr-artifacts.yml")
	job, ok := wf.Jobs["publish-pr-iso"]
	if !ok {
		t.Fatal("missing publish-pr-iso job")
	}
	if job.Name != "Publish PR BOOTy ISO artifacts" {
		t.Fatalf("publish-pr-iso name = %q", job.Name)
	}

	build := requireWorkflowStepRunContains(t, job, "Build ISO artifacts", "build_iso full iso booty.iso")
	for _, want := range []string{
		"build_iso gobgp gobgp-iso booty-gobgp.iso",
		"out-full-iso/booty.iso",
		"out-gobgp-iso/booty-gobgp.iso",
		"booty-pr-artifacts.json",
	} {
		if !strings.Contains(build.Run, want) {
			t.Fatalf("Build ISO artifacts missing %q", want)
		}
	}

	upload := requireWorkflowAction(t, job, "actions/upload-artifact")
	assertWorkflowActionPinned(t, upload, "actions/upload-artifact")
	assertWorkflowWith(t, upload, "name", "booty-pr-isos")
	for _, want := range []string{
		"out-full-iso/booty.iso",
		"out-full-iso/booty.iso.sha256",
		"out-gobgp-iso/booty-gobgp.iso",
		"out-gobgp-iso/booty-gobgp.iso.sha256",
		"booty-pr-artifacts.json",
	} {
		assertStepWithContains(t, upload, "path", want)
	}

	for _, forbidden := range []string{
		"booty-gobgp-pr-iso",
		"Publish PR BOOTy GoBGP ISO",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("pr artifact workflow still contains GoBGP-only marker %q", forbidden)
		}
	}
}
