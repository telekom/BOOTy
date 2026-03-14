//go:build linux

package provision

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ErrNoCheckpoint is returned when no checkpoint file exists.
var ErrNoCheckpoint = errors.New("no checkpoint found")

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

// LoadCheckpoint reads a checkpoint from tmpfs.
// Returns (nil, nil) if no checkpoint file exists.
func LoadCheckpoint() (*Checkpoint, error) {
	data, err := os.ReadFile(checkpointPath) //nolint:gosec // checkpoint path is a constant
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoCheckpoint
		}
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}
	return &cp, nil
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
