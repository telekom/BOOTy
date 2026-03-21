package broadcom

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

// Commander abstracts command execution for testing.
type Commander interface {
	CombinedOutput(ctx context.Context, cmd string, args ...string) ([]byte, error)
}

// OSCommander implements Commander using os/exec.
type OSCommander struct{}

// CombinedOutput runs a command and returns combined stdout/stderr.
func (OSCommander) CombinedOutput(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, cmd, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("command %s: %w", cmd, err)
	}
	return out, nil
}

// Manager handles Broadcom NIC firmware operations.
type Manager struct {
	log       *slog.Logger
	commander Commander
}

// New creates a Broadcom firmware manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log, commander: OSCommander{}}
}

// NewWithCommander creates a Broadcom firmware manager with a custom commander (for testing).
func NewWithCommander(log *slog.Logger, commander Commander) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if commander == nil {
		commander = OSCommander{}
	}
	return &Manager{log: log, commander: commander}
}

// Vendor returns the Broadcom vendor constant.
func (m *Manager) Vendor() nic.Vendor { return nic.VendorBroadcom }

// Supported checks if this is a Broadcom NIC.
func (m *Manager) Supported(n *nic.Identifier) bool {
	return n != nil && n.VendorID == "0x14e4"
}

// Capture reads firmware parameters from a Broadcom NIC via ethtool.
func (m *Manager) Capture(ctx context.Context, n *nic.Identifier) (*nic.FirmwareState, error) {
	if n == nil {
		return nil, fmt.Errorf("nic identifier required")
	}
	state := &nic.FirmwareState{
		NIC:        *n,
		Parameters: make(map[string]nic.Parameter),
	}

	if n.Interface == "" {
		return nil, fmt.Errorf("interface name required for ethtool operations")
	}

	if err := m.captureViaEthtool(ctx, n.Interface, state); err != nil {
		return nil, fmt.Errorf("capture firmware from %s: %w", n.Interface, err)
	}

	m.log.Info("captured Broadcom NIC firmware", "pci", n.PCIAddress, "params", len(state.Parameters))
	return state, nil
}

// Apply sets firmware parameters on a Broadcom NIC.
func (m *Manager) Apply(ctx context.Context, n *nic.Identifier, changes []nic.FlagChange) error {
	if n == nil {
		return fmt.Errorf("nic identifier required")
	}
	if n.Interface == "" {
		return fmt.Errorf("interface name required for ethtool operations")
	}
	for _, change := range changes {
		if err := m.applyViaEthtool(ctx, n.Interface, change); err != nil {
			return fmt.Errorf("set %s=%s: %w", change.Name, change.Value, err)
		}
	}
	return nil
}

// captureViaEthtool captures metadata and private flags via ethtool.
func (m *Manager) captureViaEthtool(ctx context.Context, iface string, state *nic.FirmwareState) error {
	// Capture read-only driver/firmware metadata via "ethtool -i"
	out, err := m.commander.CombinedOutput(ctx, "ethtool", "-i", iface)
	if err != nil {
		return fmt.Errorf("ethtool -i %s: %w", iface, err)
	}

	for _, line := range strings.Split(string(out), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "firmware-version" {
			state.FWVersion = val
		}
		state.Parameters[key] = nic.Parameter{
			Name:     key,
			Current:  val,
			ReadOnly: true,
		}
	}

	// Capture modifiable private flags via "ethtool --show-priv-flags"
	out, err = m.commander.CombinedOutput(ctx, "ethtool", "--show-priv-flags", iface)
	if err != nil {
		m.log.Debug("ethtool --show-priv-flags failed (optional)", "err", err)
		// Non-fatal: some drivers don't support private flags
		return nil
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Private flags for") {
			continue
		}
		// Format: "flag-name  : on" or "flag-name  : off"
		parts := strings.FieldsFunc(line, func(r rune) bool { return r == ':' })
		if len(parts) != 2 {
			continue
		}
		flagName := strings.TrimSpace(parts[0])
		flagValue := strings.TrimSpace(parts[1])
		state.Parameters[flagName] = nic.Parameter{
			Name:     flagName,
			Current:  flagValue,
			ReadOnly: false,
		}
	}

	return nil
}

func (m *Manager) applyViaEthtool(ctx context.Context, iface string, change nic.FlagChange) error {
	out, err := m.commander.CombinedOutput(ctx, "ethtool", "--set-priv-flags", iface, change.Name, change.Value)
	if err != nil {
		return fmt.Errorf("ethtool set-priv-flags %s %s: %s: %w", iface, change.Name, string(out), err)
	}
	return nil
}

// DriverType returns "tg3" or "bnxt_en" based on the NIC driver.
func DriverType(n *nic.Identifier) string {
	if n == nil {
		return "unknown"
	}
	switch n.Driver {
	case "tg3":
		return "tg3"
	case "bnxt_en":
		return "bnxt_en"
	default:
		return "unknown"
	}
}
