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
	return &Manager{log: log}
}

// Vendor returns the Broadcom vendor constant.
func (m *Manager) Vendor() nic.Vendor { return nic.VendorBroadcom }

// Supported checks if this is a Broadcom NIC.
func (m *Manager) Supported(n *nic.Identifier) bool {
	return n.VendorID == "0x14e4"
}

// Capture reads firmware parameters from a Broadcom NIC via ethtool.
func (m *Manager) Capture(n *nic.Identifier) (*nic.FirmwareState, error) {
	state := &nic.FirmwareState{
		NIC:        *n,
		Parameters: make(map[string]nic.Parameter),
	}

	if n.Interface != "" {
		if err := m.captureViaEthtool(context.Background(), n.Interface, state); err != nil {
			m.log.Warn("ethtool capture failed", "err", err)
		}
	}

	m.log.Info("captured Broadcom NIC firmware", "pci", n.PCIAddress, "params", len(state.Parameters))
	return state, nil
}

// Apply sets firmware parameters on a Broadcom NIC.
func (m *Manager) Apply(n *nic.Identifier, changes []nic.FlagChange) error {
	for _, change := range changes {
		if err := m.applyViaEthtool(context.Background(), n.Interface, change); err != nil {
			return fmt.Errorf("set %s=%s: %w", change.Name, change.Value, err)
		}
	}
	return nil
}

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
