//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"
)

// RetryPolicy defines how a provisioning step handles failures.
type RetryPolicy struct {
	MaxAttempts  int           // 0 = no retry
	InitialDelay time.Duration // base delay before first retry
	MaxDelay     time.Duration // cap on exponential backoff
	Jitter       float64       // 0.0-1.0, random delay fraction
	Transient    bool          // if true, errors are assumed transient
}

// DefaultPolicies maps step names to their retry policies.
var DefaultPolicies = map[string]RetryPolicy{
	"report-init":           {MaxAttempts: 5, InitialDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Jitter: 0.2, Transient: true},
	"configure-dns":         {MaxAttempts: 5, InitialDelay: 1 * time.Second, MaxDelay: 15 * time.Second, Jitter: 0.1, Transient: true},
	"stream-image":          {MaxAttempts: 3, InitialDelay: 5 * time.Second, MaxDelay: 60 * time.Second, Jitter: 0.3, Transient: true},
	"detect-disk":           {MaxAttempts: 3, InitialDelay: 2 * time.Second, MaxDelay: 10 * time.Second, Jitter: 0.1, Transient: true},
	"partprobe":             {MaxAttempts: 3, InitialDelay: 1 * time.Second, MaxDelay: 5 * time.Second, Jitter: 0.0, Transient: true},
	"report-success":        {MaxAttempts: 5, InitialDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Jitter: 0.2, Transient: true},
	"create-efi-boot-entry": {MaxAttempts: 2, InitialDelay: 1 * time.Second, MaxDelay: 5 * time.Second, Jitter: 0.0, Transient: true},
}

// WithRetry executes fn with the given retry policy.
func WithRetry(ctx context.Context, name string, policy RetryPolicy, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := range policy.MaxAttempts + 1 {
		if attempt > 0 {
			delay := backoffDelay(policy, attempt)
			slog.Warn("Retrying step", "step", name, "attempt", attempt, "delay", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return fmt.Errorf("retry canceled for %s: %w", name, ctx.Err())
			}
		}

		if err := fn(ctx); err != nil {
			lastErr = err
			if !isTransient(err) && !policy.Transient {
				return fmt.Errorf("%s: permanent failure: %w", name, err)
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("%s: exhausted %d attempts: %w", name, policy.MaxAttempts+1, lastErr)
}

func backoffDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := time.Duration(float64(policy.InitialDelay) * math.Pow(2, float64(attempt-1)))
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if policy.Jitter > 0 {
		jitter := time.Duration(float64(delay) * policy.Jitter * rand.Float64()) //nolint:gosec // jitter does not need crypto-grade randomness
		delay += jitter
	}
	return delay
}
