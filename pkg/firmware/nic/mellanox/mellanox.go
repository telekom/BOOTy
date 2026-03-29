//go:build linux

package mellanox

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

// Commander is an alias for nic.Commander for backward compatibility.
type Commander = nic.Commander

// OSCommander is an alias for nic.OSCommander for backward compatibility.
type OSCommander = nic.OSCommander

// Manager handles Mellanox ConnectX NIC firmware operations.
type Manager struct {
	log       *slog.Logger
	commander Commander
}

// New creates a Mellanox firmware manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log, commander: OSCommander{}}
}

// NewWithCommander creates a Mellanox firmware manager with a custom commander (for testing).
func NewWithCommander(log *slog.Logger, commander Commander) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if commander == nil {
		commander = OSCommander{}
	}
	return &Manager{log: log, commander: commander}
}

// Vendor returns the Mellanox vendor constant.
func (m *Manager) Vendor() nic.Vendor { return nic.VendorMellanox }

// Supported checks if this is a Mellanox NIC.
func (m *Manager) Supported(n *nic.Identifier) bool {
	return n != nil && n.VendorID == "0x15b3"
}

// Capture reads firmware parameters from a Mellanox NIC via mstconfig.
func (m *Manager) Capture(ctx context.Context, n *nic.Identifier) (*nic.FirmwareState, error) {
	if n == nil {
		return nil, fmt.Errorf("nic identifier required")
	}
	state := &nic.FirmwareState{
		NIC:        *n,
		Parameters: make(map[string]nic.Parameter),
	}

	// Try mstconfig query
	out, err := m.mstconfigQuery(ctx, n.PCIAddress)
	if err != nil {
		return nil, fmt.Errorf("mstconfig query: %w", err)
	}

	for _, line := range strings.Split(out, "\n") {
		name, param, ok := parseMstconfigLine(line)
		if ok {
			state.Parameters[name] = param
		}
	}

	m.log.Info("captured Mellanox NIC firmware", "pci", n.PCIAddress, "params", len(state.Parameters))
	return state, nil
}

// Apply sets firmware parameters on a Mellanox NIC via mstconfig.
func (m *Manager) Apply(ctx context.Context, n *nic.Identifier, changes []nic.FlagChange) error {
	if n == nil {
		return fmt.Errorf("nic identifier required")
	}
	for _, change := range changes {
		if err := m.mstconfigSet(ctx, n.PCIAddress, change.Name, change.Value); err != nil {
			return fmt.Errorf("set %s=%s: %w", change.Name, change.Value, err)
		}
		m.log.Info("applied Mellanox FW param", "param", change.Name, "value", change.Value)
	}
	return nil
}

func (m *Manager) mstconfigQuery(ctx context.Context, pciAddr string) (string, error) {
	out, err := m.commander.CombinedOutput(ctx, "mstconfig", "-d", pciAddr, "query")
	if err != nil {
		return "", fmt.Errorf("mstconfig query %s: %w", pciAddr, err)
	}
	return string(out), nil
}

func (m *Manager) mstconfigSet(ctx context.Context, pciAddr, param, value string) error {
	out, err := m.commander.CombinedOutput(ctx, "mstconfig", "-d", pciAddr, "-y", "set", param+"="+value)
	if err != nil {
		return fmt.Errorf("mstconfig set %s: %s: %w", param, string(out), err)
	}
	return nil
}

// parseMstconfigLine parses a single line of mstconfig query output.
// Format: "PARAM_NAME                  VALUE(DEFAULT)".
func parseMstconfigLine(line string) (string, nic.Parameter, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "Device") || strings.HasPrefix(line, "Config") {
		return "", nic.Parameter{}, false
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", nic.Parameter{}, false
	}

	name := fields[0]
	rawVal := fields[1]
	current := rawVal
	def := ""

	// Handle "VALUE(DEFAULT)" format — e.g., "True(True)"
	if idx := strings.IndexByte(rawVal, '('); idx >= 0 {
		current = rawVal[:idx]
		def = strings.TrimSuffix(rawVal[idx+1:], ")")
	} else if len(fields) >= 3 {
		def = strings.Trim(fields[2], "()")
	}

	return name, nic.Parameter{
		Name:    name,
		Current: current,
		Default: def,
	}, true
}

// CriticalParams returns the list of critical Mellanox firmware parameters.
func CriticalParams() []string {
	return []string{
		"SRIOV_EN",
		"NUM_OF_VFS",
		"LINK_TYPE_P1",
		"LINK_TYPE_P2",
		"ROCE_MODE",
		"PCI_WR_ORDERING",
		"CQE_COMPRESSION",
	}
}
