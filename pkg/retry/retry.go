package retry

import (
	"context"
	"fmt"
	"time"
)

// Policy configures retry attempts and exponential backoff behavior.
type Policy struct {
	Attempts       int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration

	wait waitFunc
}

type waitFunc func(ctx context.Context, delay time.Duration) error

// AttemptFunc executes one attempt.
// The bool return indicates whether the error is retryable.
type AttemptFunc func(attempt int) (bool, error)

// RetryHook runs before waiting for the next retry.
type RetryHook func(attempt int, backoff time.Duration, err error)

// Do executes fn according to policy.
func Do(ctx context.Context, policy Policy, fn AttemptFunc, onRetry RetryHook) error {
	policy = normalizePolicy(policy)

	var lastErr error
	for attempt := 1; attempt <= policy.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled: %w", err)
		}

		retryable, err := fn(attempt)
		if err == nil {
			return nil
		}

		lastErr = err
		if !retryable || attempt == policy.Attempts {
			return lastErr
		}

		delay := backoffForAttempt(policy, attempt)
		if onRetry != nil {
			onRetry(attempt, delay, err)
		}

		if delay <= 0 {
			continue
		}

		if err := policy.wait(ctx, delay); err != nil {
			return fmt.Errorf("context canceled: %w", err)
		}
	}

	return lastErr
}

func normalizePolicy(policy Policy) Policy {
	if policy.Attempts <= 0 {
		policy.Attempts = 1
	}
	if policy.InitialBackoff < 0 {
		policy.InitialBackoff = 0
	}
	if policy.MaxBackoff < 0 {
		policy.MaxBackoff = 0
	}
	if policy.wait == nil {
		policy.wait = waitWithContext
	}
	return policy
}

func backoffForAttempt(policy Policy, attempt int) time.Duration {
	delay := policy.InitialBackoff
	if delay <= 0 {
		return 0
	}

	const maxDuration = time.Duration(1<<63 - 1)
	for i := 1; i < attempt; i++ {
		if delay > maxDuration/2 {
			delay = maxDuration
		} else {
			delay *= 2
		}
		if policy.MaxBackoff > 0 && delay >= policy.MaxBackoff {
			return policy.MaxBackoff
		}
	}

	if policy.MaxBackoff > 0 && delay > policy.MaxBackoff {
		delay = policy.MaxBackoff
	}

	return delay
}

func waitWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait canceled: %w", ctx.Err())
	}
}
