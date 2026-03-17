//go:build linux

package provision

import (
	"errors"
	"fmt"
	"testing"
)

func TestTransientError(t *testing.T) {
	inner := errors.New("network timeout")
	err := &TransientError{Err: inner}

	if err.Error() != "network timeout" {
		t.Errorf("got %q, want %q", err.Error(), "network timeout")
	}
	if !errors.Is(err, inner) {
		t.Error("Unwrap should return inner error")
	}
}

func TestPermanentError(t *testing.T) {
	inner := errors.New("disk not found")
	err := &PermanentError{Err: inner}

	if err.Error() != "disk not found" {
		t.Errorf("got %q, want %q", err.Error(), "disk not found")
	}
	if !errors.Is(err, inner) {
		t.Error("Unwrap should return inner error")
	}
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"transient error", &TransientError{Err: errors.New("timeout")}, true},
		{"permanent error", &PermanentError{Err: errors.New("disk")}, false},
		{"plain error", errors.New("generic"), false},
		{"wrapped transient", fmt.Errorf("wrap: %w", &TransientError{Err: errors.New("t")}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient(tc.err); got != tc.want {
				t.Errorf("isTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsPermanent(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"permanent error", &PermanentError{Err: errors.New("disk")}, true},
		{"transient error", &TransientError{Err: errors.New("timeout")}, false},
		{"plain error", errors.New("generic"), false},
		{"wrapped permanent", fmt.Errorf("wrap: %w", &PermanentError{Err: errors.New("p")}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanent(tc.err); got != tc.want {
				t.Errorf("isPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestTransientError_NilErr(t *testing.T) {
	err := &TransientError{}
	if err.Error() != "transient error" {
		t.Errorf("got %q, want %q", err.Error(), "transient error")
	}
	if err.Unwrap() != nil {
		t.Error("expected nil from Unwrap")
	}
}

func TestPermanentError_NilErr(t *testing.T) {
	err := &PermanentError{}
	if err.Error() != "permanent error" {
		t.Errorf("got %q, want %q", err.Error(), "permanent error")
	}
	if err.Unwrap() != nil {
		t.Error("expected nil from Unwrap")
	}
}
