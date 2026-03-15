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
	path              string   // configurable checkpoint file path
}

const defaultCheckpointPath = "/tmp/booty-checkpoint.json"

// Save writes the current checkpoint to tmpfs.
func (c *Checkpoint) Save() error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	p := c.path
	if p == "" {
		p = defaultCheckpointPath
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint reads a checkpoint from tmpfs.
// Returns (nil, ErrNoCheckpoint) if no checkpoint file exists.
func LoadCheckpoint() (*Checkpoint, error) {
	return LoadCheckpointFrom(defaultCheckpointPath)
}

// LoadCheckpointFrom reads a checkpoint from the given path.
// Returns (nil, ErrNoCheckpoint) if the file does not exist.
func LoadCheckpointFrom(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path) //nolint:gosec // checkpoint path is a constant or explicitly provided
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
	cp.path = path
	return &cp, nil
}

// Remove deletes the checkpoint file.
func (c *Checkpoint) Remove() error {
	p := c.path
	if p == "" {
		p = defaultCheckpointPath
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove checkpoint: %w", err)
	}
	return nil
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
