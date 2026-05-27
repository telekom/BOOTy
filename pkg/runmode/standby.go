//go:build linux

package runmode

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/provision"
	"github.com/telekom/BOOTy/pkg/rescue"
)

const (
	heartbeatInterval = 30 * time.Second
	pollInterval      = 10 * time.Second
)

// StandbyMode keeps the machine in a hot-standby loop, sending periodic
// heartbeats and polling for commands.
type StandbyMode struct {
	deps Deps
}

// Name returns the mode identifier.
func (m *StandbyMode) Name() string { return "standby" }

// Run enters the standby heartbeat/command polling loop until the context is canceled.
func (m *StandbyMode) Run(ctx context.Context) error {
	slog.Info("entering standby mode")
	_ = m.deps.Client.ReportStatus(ctx, config.StatusInit, "standby")

	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("standby context canceled, shutting down")
			return ctx.Err()

		case <-heartbeatTicker.C:
			if err := m.deps.Client.Heartbeat(ctx); err != nil {
				slog.Warn("heartbeat failed", "error", err)
			}

		case <-pollTicker.C:
			cmds, err := m.deps.Client.FetchCommands(ctx)
			if err != nil {
				slog.Warn("command poll failed", "error", err)
				continue
			}
			for _, cmd := range cmds {
				result := m.handleCommand(ctx, cmd)
				if result != nil {
					return result.err
				}
			}
		}
	}
}

type commandResult struct {
	err error
}

func (m *StandbyMode) handleCommand(ctx context.Context, cmd config.Command) *commandResult {
	slog.Info("received command", "id", cmd.ID, "type", cmd.Type)

	switch cmd.Type {
	case "provision":
		return m.handleProvision(ctx, cmd)
	case "deprovision":
		return m.handleDeprovision(ctx, cmd)
	case "reboot":
		slog.Info("reboot command received")
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "completed", ""); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
		return &commandResult{err: &RebootRequestedError{}}
	case "health-check":
		slog.Info("health-check command received")
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "completed", "healthy"); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
		return nil
	default:
		slog.Warn("unknown command type", "type", cmd.Type)
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "failed", "unknown command type"); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
		return nil
	}
}

func (m *StandbyMode) handleProvision(ctx context.Context, cmd config.Command) *commandResult {
	m.deps.Cfg.Mode = "provision"
	orch := provision.NewOrchestrator(m.deps.Cfg, m.deps.Client, m.deps.DiskMgr)
	rescueCfg := orch.RescueConfig()
	var retryState rescue.RetryState
	var provErr error
	provisionSucceeded := false

	for {
		provErr = orch.Provision(ctx)
		if provErr == nil {
			provisionSucceeded = true
			break
		}
		slog.Error("hot provision failed", "error", provErr)
		if setupErr := rescue.Setup(ctx, rescueCfg); setupErr != nil {
			slog.Warn("rescue setup error", "error", setupErr)
		}
		action := rescue.Decide(rescueCfg, &retryState)
		slog.Info("rescue action", "type", action.Type, "message", action.Message)

		switch action.Type {
		case rescue.ModeRetry:
			retryState.RecordAttempt(provErr)
			if !sleepWithContext(ctx, rescueCfg.RetryDelay) {
				return &commandResult{err: ctx.Err()}
			}
			continue
		case rescue.ModeShell:
			slog.Info("dropping to rescue shell")
			return &commandResult{err: &RescueShellError{}}
		case rescue.ModeWait:
			slog.Info("waiting for manual intervention")
			<-ctx.Done()
			return &commandResult{err: ctx.Err()}
		default:
			// ModeReboot
		}
		break
	}

	if !provisionSucceeded {
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "failed", fmt.Sprintf("provision command failed: %v", provErr)); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
		return &commandResult{err: provErr}
	}

	if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "completed", ""); ackErr != nil {
		slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
	}

	return &commandResult{err: &ProvisionCompleteError{FirmwareChanged: orch.FirmwareChanged()}}
}

func (m *StandbyMode) handleDeprovision(ctx context.Context, cmd config.Command) *commandResult {
	m.deps.Cfg.Mode = "deprovision"
	orch := provision.NewOrchestrator(m.deps.Cfg, m.deps.Client, m.deps.DiskMgr)
	if err := orch.Deprovision(ctx); err != nil {
		slog.Error("hot deprovision failed", "error", err)
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "failed", fmt.Sprintf("deprovision command failed: %v", err)); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
	} else {
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "completed", ""); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
	}
	return &commandResult{err: &RebootRequestedError{}}
}
