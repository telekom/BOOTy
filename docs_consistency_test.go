package main

import (
	"os"
	"strings"
	"testing"
)

func TestReviewerDocsPointAtProvisionStepCountTest(t *testing.T) {
	for _, path := range []string{
		"CONTRIBUTING.md",
		".github/AGENTS.md",
		".github/agents/provisioning-reviewer.agent.md",
		".github/copilot-instructions.md",
		".github/instructions/review.instructions.md",
		".github/prompts/proposal.prompt.md",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if strings.Contains(text, "currently 42 steps") || strings.Contains(text, "42-step") {
			t.Fatalf("%s still contains stale step-count guidance", path)
		}
		if !strings.Contains(text, "TestProvisionStepCount") {
			t.Fatalf("%s must point reviewers at TestProvisionStepCount", path)
		}
	}
}

func TestREADMEClarifiesBondLabDoesNotProveLACP(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"not real LACP negotiation",
		"does not prove 802.3ad/LACP negotiation",
		"nightly smoke coverage for DHCP, static, multi-NIC, and bond topologies",
		"path-filtered `linux/arm64` flavor packaging",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing %q", want)
		}
	}
}
