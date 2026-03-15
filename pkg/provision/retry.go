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
	MaxRetries   int           // 0 = no retry, total attempts = MaxRetries+1
	InitialDelay time.Duration // base delay before first retry
	MaxDelay     time.Duration // cap on exponential backoff
	Jitter       float64       // 0.0-1.0, random delay fraction
	Transient    bool          // if true, errors are assumed transient
}

// DefaultPolicies maps step names to their retry policies.
var DefaultPolicies = map[string]RetryPolicy{
	"report-init":           {MaxRetries: 5, InitialDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Jitter: 0.2, Transient: true},
	"configure-dns":         {MaxRetries: 5, InitialDelay: 1 * time.Second, MaxDelay: 15 * time.Second, Jitter: 0.1, Transient: true},
	"stream-image":          {MaxRetries: 3, InitialDelay: 5 * time.Second, MaxDelay: 60 * time.Second, Jitter: 0.3, Transient: true},
	"detect-disk":           {MaxRetries: 3, InitialDelay: 2 * time.Second, MaxDelay: 10 * time.Second, Jitter: 0.1, Transient: true},
	"partprobe":             {MaxRetries: 3, InitialDelay: 1 * time.Second, MaxDelay: 5 * time.Second, Jitter: 0.0, Transient: true},
	"report-success":        {MaxRetries: 5, InitialDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Jitter: 0.2, Transient: true},
	"create-efi-boot-entry": {MaxRetries: 2, InitialDelay: 1 * time.Second, MaxDelay: 5 * time.Second, Jitter: 0.0, Transient: true},
}

// WithRetry executes fn with the given retry policy.
func WithRetry(ctx context.Context, name string, policy RetryPolicy, fn func(ctx context.Context) error) error {
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	}
	if policy.Jitter < 0 {
		policy.Jitter = 0
	}
	if policy.Jitter > 1 {
		policy.Jitter = 1
	}
	var lastErr error
	for attempt := range policy.MaxRetries + 1 {
		if attempt > 0 {
			delay := backoffDelay(policy, attempt)
			slog.Warn("Retrying step", "step", name, "attempt", attempt, "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("retry canceled: %w", ctx.Err())
			}
		}

		if err := fn(ctx); err != nil {
			lastErr = err
			if isPermanent(err) {
				return fmt.Errorf("permanent failure: %w", err)
			}
			if !isTransient(err) && !policy.Transient {
				return fmt.Errorf("non-transient failure: %w", err)
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("exhausted %d retries: %w", policy.MaxRetries, lastErr)
}

func backoffDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := time.Duration(float64(policy.InitialDelay) * math.Pow(2, float64(attempt-1)))
	if policy.MaxDelay > 0 && delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if policy.Jitter > 0 {
		jitter := time.Duration(float64(delay) * policy.Jitter * rand.Float64()) //nolint:gosec // jitter does not need crypto-grade randomness
		delay += jitter
	}
	return delay
}
