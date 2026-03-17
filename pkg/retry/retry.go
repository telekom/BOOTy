// Package retry provides a simple retry utility with exponential backoff.
package retry

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Config holds retry configuration.
type Config struct {
	MaxAttempts int
	InitialWait time.Duration
	MaxWait     time.Duration
	Multiplier  float64
}

// DefaultConfig returns a reasonable default retry configuration.
func DefaultConfig() Config {
	return Config{
		MaxAttempts: 3,
		InitialWait: time.Second,
		MaxWait:     30 * time.Second,
		Multiplier:  2.0,
	}
}

// Do retries fn up to cfg.MaxAttempts times with exponential backoff.
// Returns the last error if all attempts fail.
func Do(ctx context.Context, cfg Config, fn func() error) error {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2.0
	}

	wait := cfg.InitialWait
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("retry cancelled: %w", err)
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if attempt == cfg.MaxAttempts {
			break
		}

		slog.Debug("retrying after error",
			"attempt", attempt,
			"maxAttempts", cfg.MaxAttempts,
			"wait", wait,
			"error", lastErr)

		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(wait):
		}

		wait = time.Duration(float64(wait) * cfg.Multiplier)
		if wait > cfg.MaxWait {
			wait = cfg.MaxWait
		}
	}

	return fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, lastErr)
}
