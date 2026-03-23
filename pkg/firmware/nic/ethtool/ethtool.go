//go:build linux

package ethtool

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
// On failure the error includes truncated command output for diagnostics.
func (OSCommander) CombinedOutput(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, cmd, args...).CombinedOutput()
	if err != nil {
		raw := strings.TrimSpace(string(out))
		if len(raw) > 512 {
			raw = raw[:512] + "...(truncated)"
		}
		if raw != "" {
			return out, fmt.Errorf("command %s: %w [output: %s]", cmd, err, raw)
		}
		return out, fmt.Errorf("command %s: %w", cmd, err)
	}
	return out, nil
}

// Manager provides a shared ethtool-based NIC firmware implementation
// for vendors that use ethtool (Broadcom, Intel).
type Manager struct {
	log       *slog.Logger
	vendor    nic.Vendor
	vendorID  string
	commander Commander
}

// New creates an ethtool-based Manager for the given vendor.
func New(vendor nic.Vendor, vendorID string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log, vendor: vendor, vendorID: vendorID, commander: OSCommander{}}
}

// NewWithCommander creates an ethtool-based Manager with a custom Commander.
func NewWithCommander(vendor nic.Vendor, vendorID string, log *slog.Logger, cmd Commander) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if cmd == nil {
		cmd = OSCommander{}
	}
	return &Manager{log: log, vendor: vendor, vendorID: vendorID, commander: cmd}
}

// Vendor returns the NIC vendor this manager handles.
func (m *Manager) Vendor() nic.Vendor { return m.vendor }

// Supported checks if this manager can handle the given NIC.
func (m *Manager) Supported(n *nic.Identifier) bool {
	return n != nil && n.VendorID == m.vendorID
}

// Capture reads firmware parameters from a NIC via ethtool.
func (m *Manager) Capture(ctx context.Context, n *nic.Identifier) (*nic.FirmwareState, error) {
	if n == nil {
		return nil, fmt.Errorf("nic identifier required")
	}
	if n.Interface == "" {
		return nil, fmt.Errorf("interface name required for ethtool operations")
	}
	state := &nic.FirmwareState{
		NIC:        *n,
		Parameters: make(map[string]nic.Parameter),
	}
	if err := m.CaptureViaEthtool(ctx, n.Interface, state); err != nil {
		return nil, fmt.Errorf("capture firmware from %s: %w", n.Interface, err)
	}
	m.log.Info("captured NIC firmware", "vendor", m.vendor, "pci", n.PCIAddress, "params", len(state.Parameters))
	return state, nil
}

// Apply sets firmware parameters on a NIC via ethtool.
func (m *Manager) Apply(ctx context.Context, n *nic.Identifier, changes []nic.FlagChange) error {
	if n == nil {
		return fmt.Errorf("nic identifier required")
	}
	if n.Interface == "" {
		return fmt.Errorf("interface name required for ethtool operations")
	}
	for _, change := range changes {
		if err := m.ApplyViaEthtool(ctx, n.Interface, change); err != nil {
			return fmt.Errorf("set %s=%s: %w", change.Name, change.Value, err)
		}
	}
	return nil
}

// CaptureViaEthtool captures metadata and private flags via ethtool.
func (m *Manager) CaptureViaEthtool(ctx context.Context, iface string, state *nic.FirmwareState) error {
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

	out, err = m.commander.CombinedOutput(ctx, "ethtool", "--show-priv-flags", iface)
	if err != nil {
		m.log.Debug("ethtool --show-priv-flags failed (optional)", "error", err)
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Private flags for") {
			continue
		}
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

// ApplyViaEthtool sets a private flag via ethtool.
func (m *Manager) ApplyViaEthtool(ctx context.Context, iface string, change nic.FlagChange) error {
	out, err := m.commander.CombinedOutput(ctx, "ethtool", "--set-priv-flags", iface, change.Name, change.Value)
	if err != nil {
		return fmt.Errorf("ethtool set-priv-flags %s %s: %s: %w", iface, change.Name, string(out), err)
	}
	return nil
}
