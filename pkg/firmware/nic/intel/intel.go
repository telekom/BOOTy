package intel

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

// Manager handles Intel NIC firmware operations.
type Manager struct {
	log *slog.Logger
}

// New creates an Intel firmware manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log}
}

// Vendor returns the Intel vendor constant.
func (m *Manager) Vendor() nic.Vendor { return nic.VendorIntel }

// Supported checks if this is an Intel NIC.
func (m *Manager) Supported(n *nic.Identifier) bool {
	return n.VendorID == "0x8086"
}

// Capture reads firmware parameters from an Intel NIC via ethtool.
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

	m.log.Info("captured Intel NIC firmware", "pci", n.PCIAddress, "params", len(state.Parameters))
	return state, nil
}

// Apply sets firmware parameters on an Intel NIC.
func (m *Manager) Apply(n *nic.Identifier, changes []nic.FlagChange) error {
	if n.Interface == "" {
		return fmt.Errorf("interface name required for ethtool operations")
	}
	for _, change := range changes {
		if err := m.applyViaEthtool(context.Background(), n.Interface, change); err != nil {
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

// DisableFWLLDP disables the firmware-managed LLDP agent on Intel ice NICs.
func (m *Manager) DisableFWLLDP(ctx context.Context, iface string) error {
	return m.applyViaEthtool(ctx, iface, nic.FlagChange{
		Name:  "disable-fw-lldp",
		Value: "on",
	})
}

// CriticalParams returns the list of critical Intel firmware parameters.
func CriticalParams() []string {
	return []string{
		"disable-fw-lldp",
		"link-down-on-close",
		"channel-inline-flow-director",
	}
}
