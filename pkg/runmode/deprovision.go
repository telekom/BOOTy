//go:build linux

package runmode

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/provision"
)

// DeprovisionMode wipes or soft-disables the installed OS.
type DeprovisionMode struct {
	deps Deps
}

// Name returns the mode identifier.
func (m *DeprovisionMode) Name() string { return "deprovision" }

// Run executes the deprovisioning pipeline.
func (m *DeprovisionMode) Run(ctx context.Context) error {
	orch := provision.NewOrchestrator(m.deps.Cfg, m.deps.Client, m.deps.DiskMgr)
	if err := orch.Deprovision(ctx); err != nil {
		slog.Error("deprovisioning failed", "error", err)
		return err
	}
	return nil
}
