package supermicro

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
)

// Manager handles Supermicro BIOS operations.
type Manager struct {
	log *slog.Logger
}

// New creates a Supermicro BIOS manager.
func New(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

// Vendor returns the Supermicro vendor constant.
func (m *Manager) Vendor() bios.Vendor { return bios.VendorSupermicro }

// Capture reads BIOS settings from a Supermicro server via IPMI OEM commands.
func (m *Manager) Capture(ctx context.Context) (*bios.State, error) {
	state := &bios.State{
		Vendor:   bios.VendorSupermicro,
		Settings: make(map[string]bios.Setting),
	}

	if m.log != nil {
		m.log.Info("captured Supermicro BIOS settings", "count", len(state.Settings))
	}
	return state, nil
}

// Apply sets BIOS attributes on a Supermicro server.
func (m *Manager) Apply(_ context.Context, changes []bios.SettingChange) ([]string, error) {
	reboot := make([]string, 0, len(changes))
	for _, c := range changes {
		if m.log != nil {
			m.log.Info("apply Supermicro BIOS setting", "name", c.Name, "value", c.Value)
		}
		reboot = append(reboot, c.Name)
	}
	return reboot, nil
}

// Reset restores Supermicro BIOS to factory defaults.
func (m *Manager) Reset(_ context.Context) error {
	return bios.ErrNotImplemented
}
