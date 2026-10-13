//go:build linux

package runmode

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	deps             Deps
	newDeprovisioner func() standbyDeprovisioner
}

type standbyDeprovisioner interface {
	Deprovision(context.Context) error
}

// Name returns the mode identifier.
func (m *StandbyMode) Name() string { return "standby" }

// Run enters the standby heartbeat/command polling loop until the context is canceled.
func (m *StandbyMode) Run(ctx context.Context) error {
	if err := m.validateConfig(); err != nil {
		return err
	}

	slog.Info("entering standby mode")
	if err := m.deps.Client.ReportStatus(ctx, config.StatusInit, "standby"); err != nil {
		return fmt.Errorf("standby status readiness: %w", err)
	}
	if err := m.deps.Client.Heartbeat(ctx); err != nil {
		return fmt.Errorf("standby heartbeat readiness: %w", err)
	}
	if result, handled, err := m.pollCommands(ctx); err != nil {
		return fmt.Errorf("standby command readiness: %w", err)
	} else if handled {
		return result.err
	}

	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("standby context canceled, shutting down")
			return fmt.Errorf("standby canceled: %w", ctx.Err())

		case <-heartbeatTicker.C:
			if err := m.deps.Client.Heartbeat(ctx); err != nil {
				slog.Warn("heartbeat failed", "error", err)
			}

		case <-pollTicker.C:
			result, handled, err := m.pollCommands(ctx)
			if err != nil {
				slog.Warn("command poll failed", "error", err)
				continue
			}
			if handled {
				return result.err
			}
		}
	}
}

func (m *StandbyMode) validateConfig() error {
	if m.deps.Cfg == nil {
		return fmt.Errorf("standby mode requires machine config")
	}
	if m.deps.Client == nil {
		return fmt.Errorf("standby mode requires CAPRF client")
	}

	var missing []string
	if strings.TrimSpace(m.deps.Cfg.Agent.HeartbeatURL) == "" {
		missing = append(missing, "HEARTBEAT_URL")
	}
	if strings.TrimSpace(m.deps.Cfg.Agent.CommandsURL) == "" {
		missing = append(missing, "COMMANDS_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("standby mode requires %s", strings.Join(missing, " and "))
	}
	return nil
}

func (m *StandbyMode) pollCommands(ctx context.Context) (*commandResult, bool, error) {
	cmds, err := m.deps.Client.FetchCommands(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, cmd := range cmds {
		result := m.handleCommand(ctx, cmd)
		if result != nil {
			return result, true, nil
		}
	}
	return nil, false, nil
}

type commandResult struct {
	err error
}

func (m *StandbyMode) handleCommand(ctx context.Context, cmd config.Command) *commandResult {
	slog.Info("received command", "id", cmd.ID, "type", cmd.Type)

	switch cmd.Type {
	case "provision", "deprovision":
		return m.handleRunCommand(ctx, cmd)
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

// commandConfigProvider is the optional provider capability that merges a
// command payload over the base vars (caprf.Client.CommandConfig).
type commandConfigProvider interface {
	CommandConfig(payload []byte) (*config.MachineConfig, []string, error)
}

// handleRunCommand runs a provision or deprovision command. A command
// payload is merged over the base vars for this command only. A result of
// nil means that standby goes on, with the base config restored.
func (m *StandbyMode) handleRunCommand(ctx context.Context, cmd config.Command) *commandResult {
	restore, keys, err := m.applyPayload(cmd.Payload)
	if err != nil {
		slog.Error("rejected command payload", "id", cmd.ID, "type", cmd.Type, "error", err)
		m.ack(ctx, cmd.ID, "failed", fmt.Sprintf("invalid %s payload: %v", cmd.Type, err))
		return nil
	}
	if len(keys) > 0 {
		slog.Info("running command with payload", "id", cmd.ID, "type", cmd.Type,
			"keys", strings.Join(keys, ","), "hostname", m.deps.Cfg.Hostname)
	}
	var result *commandResult
	if cmd.Type == "provision" {
		result = m.handleProvision(ctx, cmd, keys)
	} else {
		result = m.handleDeprovision(ctx, cmd, keys)
	}
	if result == nil {
		restore()
	}
	return result
}

// applyPayload replaces the config with the base vars plus the payload. The
// settings that the running ramdisk uses (transport, agent, network) keep
// their live values. restore puts the config of the standby loop back.
func (m *StandbyMode) applyPayload(payload []byte) (restore func(), keys []string, err error) {
	live := m.deps.Cfg
	base := *live
	restore = func() { *live = base }
	if len(bytes.TrimSpace(payload)) == 0 {
		return restore, nil, nil
	}
	p, ok := m.deps.Client.(commandConfigProvider)
	if !ok {
		return nil, nil, fmt.Errorf("the provider does not support command payloads")
	}
	runCfg, keys, err := p.CommandConfig(payload)
	if err != nil {
		return nil, nil, err
	}
	runCfg.Transport = live.Transport
	runCfg.Agent = live.Agent
	runCfg.Network = live.Network
	runCfg.ProvisionerFiles = live.ProvisionerFiles
	runCfg.MachineFiles = live.MachineFiles
	runCfg.MachineCommands = live.MachineCommands
	*live = *runCfg
	return restore, keys, nil
}

// ack reports a command result. A failed ACK is logged only.
func (m *StandbyMode) ack(ctx context.Context, id, status, message string) {
	if err := m.deps.Client.AcknowledgeCommand(ctx, id, status, message); err != nil {
		slog.Warn("failed to ACK command", "cmdID", id, "error", err)
	}
}

// ackMessage is the message of a completed command ACK.
func ackMessage(done string, keys []string) string {
	if len(keys) == 0 {
		return done
	}
	return done + " with payload keys " + strings.Join(keys, ",")
}

func (m *StandbyMode) handleProvision(ctx context.Context, cmd config.Command, keys []string) *commandResult {
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
				return &commandResult{err: fmt.Errorf("retry canceled: %w", ctx.Err())}
			}
			continue
		case rescue.ModeShell:
			slog.Info("dropping to rescue shell")
			return &commandResult{err: &RescueShellError{}}
		case rescue.ModeWait:
			// ACK the failure first: standby reports every failed command.
			if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "failed", fmt.Sprintf("provision command failed: %v", provErr)); ackErr != nil {
				slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
			}
			slog.Info("waiting for manual intervention")
			<-ctx.Done()
			return &commandResult{err: fmt.Errorf("wait mode canceled: %w", ctx.Err())}
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

	m.ack(ctx, cmd.ID, "completed", ackMessage("provisioning complete", keys))

	return &commandResult{err: provisionComplete(orch.FirmwareChanged(), m.deps.Cfg.PowerOffOnSuccess())}
}

func (m *StandbyMode) handleDeprovision(ctx context.Context, cmd config.Command, keys []string) *commandResult {
	m.deps.Cfg.Mode = "deprovision"
	orch := m.deprovisioner()
	if err := orch.Deprovision(ctx); err != nil {
		slog.Error("hot deprovision failed", "error", err)
		if ackErr := m.deps.Client.AcknowledgeCommand(ctx, cmd.ID, "failed", fmt.Sprintf("deprovision command failed: %v", err)); ackErr != nil {
			slog.Warn("failed to ACK command", "cmdID", cmd.ID, "error", ackErr)
		}
		if m.deps.Cfg.WaitOnFailure() {
			slog.Info("waiting after deprovisioning failure")
			<-ctx.Done()
		}
		return &commandResult{err: err}
	}

	m.ack(ctx, cmd.ID, "completed", ackMessage("deprovisioning complete", keys))
	if m.deps.Cfg.Agent.DeprovisionSuccessAction == config.AgentDeprovisionStandby {
		slog.Info("deprovisioning complete, returning to standby")
		if err := m.deps.Client.ReportStatus(ctx, config.StatusInit, "standby"); err != nil {
			slog.Warn("standby status after deprovisioning failed", "error", err)
		}
		return nil
	}
	if m.deps.Cfg.PowerOffOnSuccess() {
		return &commandResult{err: &PowerOffRequestedError{}}
	}
	return &commandResult{err: &RebootRequestedError{}}
}

func (m *StandbyMode) deprovisioner() standbyDeprovisioner {
	if m.newDeprovisioner != nil {
		return m.newDeprovisioner()
	}
	return provision.NewOrchestrator(m.deps.Cfg, m.deps.Client, m.deps.DiskMgr)
}
