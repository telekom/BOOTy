//go:build linux

package provision

import (
	"encoding/json"
	"fmt"
	"os"
)

// Checkpoint records provisioning progress to tmpfs.
type Checkpoint struct {
	LastCompletedStep string   `json:"lastCompletedStep"`
	CompletedSteps    []string `json:"completedSteps"`
	AttemptCount      int      `json:"attemptCount"`
	Errors            []string `json:"errors,omitempty"`
}

const checkpointPath = "/tmp/booty-checkpoint.json"

// Save writes the current checkpoint to tmpfs.
func (c *Checkpoint) Save() error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if err := os.WriteFile(checkpointPath, data, 0o600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint reads a checkpoint from tmpfs (returns nil if none exists).
func LoadCheckpoint() *Checkpoint {
	data, err := os.ReadFile(checkpointPath) //nolint:gosec // checkpoint path is a constant
	if err != nil {
		return nil
	}
	var cp Checkpoint
	if json.Unmarshal(data, &cp) != nil {
		return nil
	}
	return &cp
}

// MarkStep records a completed step in the checkpoint.
func (c *Checkpoint) MarkStep(name string) {
	c.LastCompletedStep = name
	c.CompletedSteps = append(c.CompletedSteps, name)
}

// IsCompleted returns true if the named step was already completed.
func (c *Checkpoint) IsCompleted(name string) bool {
	for _, s := range c.CompletedSteps {
		if s == name {
			return true
		}
	}
	return false
}
