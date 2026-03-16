package broadcom

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

// Manager handles Broadcom NIC firmware operations.
type Manager struct {
	log *slog.Logger
}

// New creates a Broadcom firmware manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log}
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

	if n.Interface != "" {
		if err := m.captureViaEthtool(ctx, n.Interface, state); err != nil {
			m.log.Warn("ethtool capture failed", "err", err)
		}
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

// captureViaEthtool captures metadata via "ethtool -i".
// NOTE: this captures driver/firmware metadata (read-only), not the priv-flags
// that Apply modifies via --set-priv-flags. A future enhancement should also
// capture --show-priv-flags output for baseline/diff to be effective.
func (m *Manager) captureViaEthtool(ctx context.Context, iface string, state *nic.FirmwareState) error {
	cmd := exec.CommandContext(ctx, "ethtool", "-i", iface)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ethtool -i %s: %s: %w", iface, string(out), err)
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
	return nil
}

func (m *Manager) applyViaEthtool(ctx context.Context, iface string, change nic.FlagChange) error {
	cmd := exec.CommandContext(ctx, "ethtool", "--set-priv-flags", iface, change.Name, change.Value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ethtool set-priv-flags %s %s: %s: %w", iface, change.Name, string(out), err)
	}
	return nil
}

// DriverType returns "tg3" or "bnxt_en" based on the NIC driver.
func DriverType(n *nic.Identifier) string {
	switch n.Driver {
	case "tg3":
		return "tg3"
	case "bnxt_en":
		return "bnxt_en"
	default:
		return "unknown"
	}
}
