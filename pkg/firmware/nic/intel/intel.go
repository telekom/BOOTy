//go:build linux

package intel

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
	"github.com/telekom/BOOTy/pkg/firmware/nic/ethtool"
)

// Manager handles Intel NIC firmware operations.
type Manager struct {
	*ethtool.Manager
}

// New creates an Intel firmware manager.
func New(log *slog.Logger) *Manager {
	return &Manager{Manager: ethtool.New(nic.VendorIntel, "0x8086", log)}
}

// NewWithCommander creates an Intel firmware manager with a custom commander.
func NewWithCommander(log *slog.Logger, commander ethtool.Commander) *Manager {
	return &Manager{Manager: ethtool.NewWithCommander(nic.VendorIntel, "0x8086", log, commander)}
}

// DisableFWLLDP disables the firmware-managed LLDP agent on Intel ice NICs.
func (m *Manager) DisableFWLLDP(ctx context.Context, iface string) error {
	if iface == "" {
		return fmt.Errorf("interface name required")
	}
	return m.ApplyViaEthtool(ctx, iface, nic.FlagChange{
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
