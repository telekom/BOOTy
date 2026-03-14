package provision
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































































}	}		t.Error("expected nil for missing checkpoint")	if cp != nil {	cp := LoadCheckpoint()	// LoadCheckpoint uses the hardcoded path, which won't exist in testfunc TestLoadCheckpoint_Missing(t *testing.T) {}	}		t.Error("expected stream-image to not be completed")	if cp.IsCompleted("stream-image") {	}		t.Error("expected report-init to be completed")	if !cp.IsCompleted("report-init") {	}		CompletedSteps: []string{"report-init", "configure-dns"},	cp := &Checkpoint{func TestCheckpoint_IsCompleted(t *testing.T) {}	}		t.Errorf("CompletedSteps length = %d, want 2", len(cp.CompletedSteps))	if len(cp.CompletedSteps) != 2 {	}		t.Errorf("LastCompletedStep = %q, want %q", cp.LastCompletedStep, "configure-dns")	if cp.LastCompletedStep != "configure-dns" {	cp.MarkStep("configure-dns")	cp.MarkStep("report-init")	cp := &Checkpoint{}func TestCheckpoint_MarkStep(t *testing.T) {}	}		t.Errorf("got %d steps, want 3", len(result.CompletedSteps))	if len(result.CompletedSteps) != 3 {	}		t.Errorf("got %q, want %q", result.LastCompletedStep, "stream-image")	if result.LastCompletedStep != "stream-image" {	}		t.Fatalf("unmarshal: %v", err)	if err := json.Unmarshal(loaded, &result); err != nil {	var result Checkpoint	}		t.Fatalf("read: %v", err)	if err != nil {	loaded, err := os.ReadFile(path)	}		t.Fatalf("write: %v", err)	if err := os.WriteFile(path, data, 0o600); err != nil {	}		t.Fatalf("marshal: %v", err)	if err != nil {	data, err := json.Marshal(cp)	}		AttemptCount:      1,		CompletedSteps:    []string{"report-init", "configure-dns", "stream-image"},		LastCompletedStep: "stream-image",	cp := &Checkpoint{