//go:build linux

package runmode

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/provision"
)

// DryRunMode simulates provisioning without destructive operations.
type DryRunMode struct {
	deps Deps
}

func (m *DryRunMode) Name() string { return "dry-run" }

func (m *DryRunMode) Run(ctx context.Context) error {
	m.deps.Cfg.Provision.DisableKexec = true
	orch := provision.NewOrchestrator(m.deps.Cfg, m.deps.Client, m.deps.DiskMgr)
	if err := orch.DryRun(ctx); err != nil {
		slog.Error("dry-run failed", "error", err)
		return err
	}
	return nil
}
