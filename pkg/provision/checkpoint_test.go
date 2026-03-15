//go:build linux

package provision

import (
	"errors"
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
		path:              path,
	}
	if err := cp.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result, err := LoadCheckpointFrom(path)
	if err != nil {
		t.Fatalf("LoadCheckpointFrom: %v", err)
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
	dir := t.TempDir()
	cp, err := LoadCheckpointFrom(filepath.Join(dir, "nonexistent.json"))
	if !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("expected ErrNoCheckpoint, got: %v", err)
	}
	if cp != nil {
		t.Error("expected nil for missing checkpoint")
	}
}
