// Package rescue provides rescue mode for failed provisioning recovery.
package rescue

import (
	"fmt"
	"time"
)

// Mode determines what happens when provisioning fails.
type Mode string

const (
	// ModeReboot reboots the machine after failure.
	ModeReboot Mode = "reboot"
	// ModeShell drops to a debug shell.
	ModeShell Mode = "shell"
	// ModeRetry retries the provisioning.
	ModeRetry Mode = "retry"
	// ModeWait waits for manual intervention.
	ModeWait Mode = "wait"
)

// ParseMode parses a rescue mode string.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeReboot, ModeShell, ModeRetry, ModeWait:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("unknown rescue mode %q", s)
	}
}

// Config holds rescue mode configuration.
type Config struct {
	Mode          Mode          `json:"mode"`
	MaxRetries    int           `json:"maxRetries,omitempty"`
	RetryDelay    time.Duration `json:"retryDelay,omitempty"`
	ShellTimeout  time.Duration `json:"shellTimeout,omitempty"`
	SSHKeys       []string      `json:"sshKeys,omitempty"`
	NetworkConfig bool          `json:"networkConfig,omitempty"`
}

// Validate checks the rescue config.
func (c *Config) Validate() error {
	if _, err := ParseMode(string(c.Mode)); err != nil {
		return fmt.Errorf("invalid mode: %w", err)
	}
	if c.Mode == ModeRetry && c.MaxRetries < 1 {
		return fmt.Errorf("maxRetries must be >= 1 for retry mode")
	}
	if c.RetryDelay < 0 {
		return fmt.Errorf("retryDelay must be non-negative")
	}
	return nil
}

// ApplyDefaults sets default values for unset fields.
func (c *Config) ApplyDefaults() {
	if c.Mode == "" {
		c.Mode = ModeReboot
	}
	if c.MaxRetries == 0 && c.Mode == ModeRetry {
		c.MaxRetries = 3
	}
	if c.RetryDelay == 0 {
		c.RetryDelay = 30 * time.Second
	}
	if c.ShellTimeout == 0 {
		c.ShellTimeout = 30 * time.Minute
	}
}

// Action represents a rescue action to take.
type Action struct {
	Type    Mode   `json:"type"`
	Message string `json:"message"`
}

// RetryState tracks retry attempts.
type RetryState struct {
	Attempts   int       `json:"attempts"`
	MaxRetries int       `json:"maxRetries"`
	LastError  string    `json:"lastError,omitempty"`
	LastRetry  time.Time `json:"lastRetry,omitempty"`
}

// CanRetry returns whether another retry is allowed.
func (s *RetryState) CanRetry() bool {
	return s.Attempts < s.MaxRetries
}

// RecordAttempt records a retry attempt.
func (s *RetryState) RecordAttempt(err error) {
	s.Attempts++
	s.LastRetry = time.Now()
	if err != nil {
		s.LastError = err.Error()
	}
}

// Remaining returns the number of retries remaining.
func (s *RetryState) Remaining() int {
	r := s.MaxRetries - s.Attempts
	if r < 0 {
		return 0
	}
	return r
}

// Decide determines the rescue action based on config and state.
func Decide(cfg *Config, state *RetryState) Action {
	switch cfg.Mode {
	case ModeRetry:
		if state.CanRetry() {
			return Action{
				Type:    ModeRetry,
				Message: fmt.Sprintf("retrying (%d/%d)", state.Attempts+1, state.MaxRetries),
			}
		}
		return Action{
			Type:    ModeReboot,
			Message: "max retries exceeded, rebooting",
		}
	case ModeShell:
		return Action{
			Type:    ModeShell,
			Message: fmt.Sprintf("dropping to rescue shell (timeout: %s)", cfg.ShellTimeout),
		}
	case ModeWait:
		return Action{
			Type:    ModeWait,
			Message: "waiting for manual intervention",
		}
	default:
		return Action{
			Type:    ModeReboot,
			Message: "rebooting",
		}
	}
}
