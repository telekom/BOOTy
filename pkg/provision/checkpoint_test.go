//go:build linux

package provision

import (
	"errors"
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
		path:              path,
		persist:           true,
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

func TestCheckpoint_SaveNoPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	// Checkpoint without persist=true should not write to disk.
	cp := &Checkpoint{
		LastCompletedStep: "report-init",
		CompletedSteps:    []string{"report-init"},
		path:              path,
	}
	if err := cp.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no file on disk when persist=false")
	}
}

func TestCheckpoint_RemoveNoPersist(t *testing.T) {
	cp := &Checkpoint{}
	// Remove on a non-persisted checkpoint should be a no-op.
	if err := cp.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestCheckpoint_AtomicSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := &Checkpoint{
		CompletedSteps: []string{"step-1"},
		path:           path,
		persist:        true,
	}
	if err := cp.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify temp file was cleaned up (atomic rename).
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("expected temp file to be cleaned up after atomic save")
	}

	// Verify checkpoint was written.
	loaded, err := LoadCheckpointFrom(path)
	if err != nil {
		t.Fatalf("LoadCheckpointFrom: %v", err)
	}
	if len(loaded.CompletedSteps) != 1 || loaded.CompletedSteps[0] != "step-1" {
		t.Errorf("loaded steps = %v, want [step-1]", loaded.CompletedSteps)
	}
}

func TestCheckpoint_ResumeFlow(t *testing.T) {
	// Simulate a resume: create a checkpoint with some completed steps,
	// then verify IsCompleted correctly identifies them.
	cp := &Checkpoint{
		LastCompletedStep: "configure-dns",
		CompletedSteps:    []string{"report-init", "configure-dns"},
		AttemptCount:      1,
	}
	if !cp.IsCompleted("report-init") {
		t.Error("expected report-init to be completed")
	}
	if !cp.IsCompleted("configure-dns") {
		t.Error("expected configure-dns to be completed")
	}
	if cp.IsCompleted("stream-image") {
		t.Error("expected stream-image to NOT be completed")
	}

	// Mark another step and verify.
	cp.MarkStep("stream-image")
	if !cp.IsCompleted("stream-image") {
		t.Error("expected stream-image to be completed after MarkStep")
	}
	if cp.LastCompletedStep != "stream-image" {
		t.Errorf("LastCompletedStep = %q, want stream-image", cp.LastCompletedStep)
	}
}
