//go:build linux

package image

import (
	"context"
	"fmt"
	"testing"
)

func TestIsTransientFormatProbeErrorTreatsDeadlineAsTransient(t *testing.T) {
	err := fmt.Errorf("%w: %w", ErrFormatProbe, context.DeadlineExceeded)
	if !IsTransientFormatProbeError(err) {
		t.Fatal("expected deadline exceeded format probe to be transient")
	}
}

func TestIsTransientFormatProbeErrorTreatsCanceledAsPermanent(t *testing.T) {
	err := fmt.Errorf("%w: %w", ErrFormatProbe, context.Canceled)
	if IsTransientFormatProbeError(err) {
		t.Fatal("expected canceled format probe to be permanent")
	}
}
