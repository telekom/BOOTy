//go:build (linux && e2e_boot) || e2e_gobgp

package integration

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pollDeadline(t *testing.T, timeout time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	if testDeadline, ok := t.Deadline(); ok {
		capped := testDeadline.Add(-30 * time.Second)
		if capped.Before(deadline) {
			return capped
		}
	}
	return deadline
}

func pollContext(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithDeadline(context.Background(), pollDeadline(t, timeout))
}

func pollUntil(ctx context.Context, interval time.Duration, condition func(context.Context) (bool, string)) error {
	if interval <= 0 {
		return fmt.Errorf("poll interval must be positive: %s", interval)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	attempts := 0
	lastDiagnostic := "condition not yet satisfied"
	for {
		attempts++
		done, diagnostic := condition(ctx)
		if diagnostic != "" {
			lastDiagnostic = diagnostic
		}
		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("poll timed out after %d attempts: %s: %w", attempts, lastDiagnostic, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPollUntilReturnsWhenConditionSucceeds(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := pollUntil(ctx, time.Millisecond, func(context.Context) (bool, string) {
		return attempts.Add(1) >= 3, "waiting for third attempt"
	})
	if err != nil {
		t.Fatalf("pollUntil() error = %v, want nil", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("pollUntil() attempts = %d, want 3", got)
	}
}

func TestPollUntilReturnsDiagnosticOnTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := pollUntil(ctx, time.Millisecond, func(context.Context) (bool, string) {
		return false, "still waiting for log entry"
	})
	if err == nil {
		t.Fatal("pollUntil() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "still waiting for log entry") {
		t.Fatalf("pollUntil() error = %q, want diagnostic message", err)
	}
}
