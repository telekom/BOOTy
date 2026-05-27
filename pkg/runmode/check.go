//go:build linux

package runmode

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/health"
)

// CheckMode runs hardware health checks without provisioning.
type CheckMode struct {
	deps Deps
}

// Name returns the mode identifier.
func (m *CheckMode) Name() string { return "check" }

// Run executes health checks and returns a HealthCheckError if any critical check fails.
func (m *CheckMode) Run(ctx context.Context) error {
	cfg := m.deps.Cfg
	_ = m.deps.Client.ReportStatus(ctx, config.StatusInit, "health-check")

	checks := []health.Check{
		&health.DiskPresenceCheck{},
		&health.DiskIOErrorCheck{},
		&health.MemoryECCCheck{},
		&health.MinimumMemoryCheck{MinGiB: cfg.Health.MinMemoryGB},
		&health.MinimumCPUCheck{MinCPUs: cfg.Health.MinCPUs},
		&health.NICLinkStateCheck{},
		&health.ThermalStateCheck{},
	}

	results, critical := health.RunAll(ctx, checks, cfg.Health.SkipChecks)

	var failed, warned int
	for _, r := range results {
		switch {
		case r.Status == health.StatusFail && r.Severity == health.SeverityCritical:
			failed++
			slog.Error("health check failed", "check", r.Name, "severity", r.Severity, "message", r.Message)
		case r.Status == health.StatusFail:
			warned++
			slog.Warn("health check warning", "check", r.Name, "severity", r.Severity, "message", r.Message)
		case r.Status == health.StatusSkip:
			slog.Info("health check skipped", "check", r.Name)
		default:
			slog.Info("health check passed", "check", r.Name)
		}
	}

	if critical {
		msg := fmt.Sprintf("%d/%d health checks failed", failed, len(results))
		_ = m.deps.Client.ReportStatus(ctx, config.StatusError, msg)
		return &HealthCheckError{Failed: failed, Total: len(results)}
	}
	summary := fmt.Sprintf("%d/%d health checks passed", len(results)-failed-warned, len(results))
	if warned > 0 {
		summary = fmt.Sprintf("%s (%d warnings)", summary, warned)
	}
	_ = m.deps.Client.ReportStatus(ctx, config.StatusSuccess, summary)
	return nil
}
