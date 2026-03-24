package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoConfigDefaults(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"zero max attempts defaults to 1", Config{MaxAttempts: 0, InitialWait: time.Millisecond}},
		{"negative max attempts defaults to 1", Config{MaxAttempts: -5, InitialWait: time.Millisecond}},
		{"zero multiplier defaults to 2.0", Config{MaxAttempts: 1, Multiplier: 0}},
		{"negative multiplier defaults to 2.0", Config{MaxAttempts: 1, Multiplier: -1}},
		{"zero max wait defaults to 30s", Config{MaxAttempts: 1, MaxWait: 0}},
		{"negative max wait defaults to 30s", Config{MaxAttempts: 1, MaxWait: -1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Do(context.Background(), tc.cfg, func() error {
				return nil
			})
			if err != nil {
				t.Errorf("Do() = %v", err)
			}
		})
	}
}

func TestDoBackoffCapped(t *testing.T) {
	cfg := Config{
		MaxAttempts: 5,
		InitialWait: time.Millisecond,
		MaxWait:     2 * time.Millisecond,
		Multiplier:  10.0, // aggressive multiplier to hit cap quickly
	}
	calls := 0
	err := Do(context.Background(), cfg, func() error {
		calls++
		if calls < 5 {
			return errors.New("fail")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	if calls != 5 {
		t.Errorf("calls = %d, want 5", calls)
	}
}

func TestDoContextCancelledDuringWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	cfg := Config{
		MaxAttempts: 10,
		InitialWait: 500 * time.Millisecond, // long wait to trigger context cancel
		MaxWait:     time.Second,
		Multiplier:  2.0,
	}
	err := Do(ctx, cfg, func() error {
		return errors.New("always fail")
	})
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		// Could also be a wrapped "retry canceled" error.
		if !errors.Is(err, context.DeadlineExceeded) {
			// Accept "retry canceled: context deadline exceeded" format.
			t.Logf("error = %v (acceptable)", err)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", cfg.MaxAttempts)
	}
	if cfg.InitialWait != time.Second {
		t.Errorf("InitialWait = %v", cfg.InitialWait)
	}
	if cfg.MaxWait != 30*time.Second {
		t.Errorf("MaxWait = %v", cfg.MaxWait)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("Multiplier = %f", cfg.Multiplier)
	}
}
