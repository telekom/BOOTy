package retry

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDoSuccessFirstAttempt(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), Policy{Attempts: 3, InitialBackoff: time.Second, wait: noWait},
		func(_ int) (bool, error) {
			attempts++
			return false, nil
		}, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoRetriesThenSuccess(t *testing.T) {
	var gotAttempts []int
	var gotDelays []time.Duration

	err := Do(context.Background(), Policy{Attempts: 4, InitialBackoff: time.Second, wait: noWait},
		func(attempt int) (bool, error) {
			gotAttempts = append(gotAttempts, attempt)
			if attempt < 3 {
				return true, fmt.Errorf("attempt %d failed", attempt)
			}
			return false, nil
		},
		func(_ int, backoff time.Duration, _ error) {
			gotDelays = append(gotDelays, backoff)
		})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if !reflect.DeepEqual(gotAttempts, []int{1, 2, 3}) {
		t.Fatalf("attempts = %v, want [1 2 3]", gotAttempts)
	}
	if !reflect.DeepEqual(gotDelays, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("delays = %v, want [1s 2s]", gotDelays)
	}
}

func TestDoReturnsLastErrorAfterExhaustion(t *testing.T) {
	rootErr := errors.New("boom")
	attempts := 0

	err := Do(context.Background(), Policy{Attempts: 3, InitialBackoff: time.Second, wait: noWait},
		func(attempt int) (bool, error) {
			attempts++
			return true, fmt.Errorf("attempt %d: %w", attempt, rootErr)
		}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, rootErr) {
		t.Fatalf("error = %v, want root error %v", err, rootErr)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestDoStopsOnNonRetryableError(t *testing.T) {
	rootErr := errors.New("fatal")
	attempts := 0

	err := Do(context.Background(), Policy{Attempts: 5, InitialBackoff: time.Second, wait: noWait},
		func(_ int) (bool, error) {
			attempts++
			return false, rootErr
		}, nil)
	if !errors.Is(err, rootErr) {
		t.Fatalf("error = %v, want %v", err, rootErr)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoContextCanceledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := Do(ctx, Policy{Attempts: 3, InitialBackoff: time.Second, wait: func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}, func(_ int) (bool, error) {
		return true, errors.New("transient")
	}, nil)
	if err == nil {
		t.Fatal("expected context canceled error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestDoRespectsMaxBackoff(t *testing.T) {
	var gotDelays []time.Duration

	err := Do(context.Background(), Policy{
		Attempts:       4,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     3 * time.Second,
		wait:           noWait,
	}, func(attempt int) (bool, error) {
		if attempt < 4 {
			return true, errors.New("retry")
		}
		return false, nil
	}, func(_ int, backoff time.Duration, _ error) {
		gotDelays = append(gotDelays, backoff)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if !reflect.DeepEqual(gotDelays, []time.Duration{2 * time.Second, 3 * time.Second, 3 * time.Second}) {
		t.Fatalf("delays = %v, want [2s 3s 3s]", gotDelays)
	}
}

func noWait(_ context.Context, _ time.Duration) error {
	return nil
}
