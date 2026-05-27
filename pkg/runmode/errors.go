//go:build linux

package runmode

import (
	"context"
	"fmt"
	"time"
)

// RescueShellError indicates the mode exited to drop into a rescue shell.
type RescueShellError struct{}

func (e *RescueShellError) Error() string { return "rescue shell requested" }

// RebootRequestedError indicates the mode exited due to a reboot request.
type RebootRequestedError struct{}

func (e *RebootRequestedError) Error() string { return "reboot requested" }

// ProvisionCompleteError indicates standby mode completed a provision command.
type ProvisionCompleteError struct {
	FirmwareChanged bool
}

func (e *ProvisionCompleteError) Error() string { return "provision complete" }

// HealthCheckError indicates one or more critical health checks failed.
type HealthCheckError struct {
	Failed int
	Total  int
}

func (e *HealthCheckError) Error() string {
	return fmt.Sprintf("%d of %d health checks failed", e.Failed, e.Total)
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
