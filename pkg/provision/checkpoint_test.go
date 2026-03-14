//go:build linux

package provision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpoint_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := &Checkpoint{
		LastCompletedStep: "stream-image",
		CompletedSteps:    []string{"report-init", "configure-dns", "stream-image"},
		AttemptCount:      1,
	}
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result Checkpoint
	if err := json.Unmarshal(loaded, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.LastCompletedStep != "stream-image" {
		t.Errorf("got %q, want %q", result.LastCompletedStep, "stream-image")
	}
	if len(result.CompletedSteps) != 3 {
		t.Errorf("got %d steps, want 3", len(result.CompletedSteps))
	}
}

func TestCheckpoint_MarkStep(t *testing.T) {
	cp := &Checkpoint{}
	cp.MarkStep("report-init")
	cp.MarkStep("configure-dns")
	if cp.LastCompletedStep != "configure-dns" {
		t.Errorf("LastCompletedStep = %q, want %q", cp.LastCompletedStep, "configure-dns")
	}
	if len(cp.CompletedSteps) != 2 {
		t.Errorf("CompletedSteps length = %d, want 2", len(cp.CompletedSteps))
	}
}

func TestCheckpoint_IsCompleted(t *testing.T) {
	cp := &Checkpoint{
		CompletedSteps: []string{"report-init", "configure-dns"},
	}
	if !cp.IsCompleted("report-init") {
		t.Error("expected report-init to be completed")
	}
	if cp.IsCompleted("stream-image") {
		t.Error("expected stream-image to not be completed")
	}
}

func TestLoadCheckpoint_Missing(t *testing.T) {
	// LoadCheckpoint uses the hardcoded path, which won't exist in test
	cp := LoadCheckpoint()
	if cp != nil {
		t.Error("expected nil for missing checkpoint")
	}
}
