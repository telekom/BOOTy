package bios

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/system"
)

// BaseManager provides a generic BIOS Manager implementation backed by a
// static critical-settings map. Vendors only need to supply their vendor
// constant and their recommended settings; all Manager interface methods
// are handled here.
type BaseManager struct {
	log      *slog.Logger
	vendor   system.Vendor
	settings map[string]string
}

// NewBaseManager creates a BaseManager for the given vendor and settings.
func NewBaseManager(vendor system.Vendor, log *slog.Logger, settings map[string]string) *BaseManager {
	if log == nil {
		log = slog.Default()
	}
	return &BaseManager{log: log, vendor: vendor, settings: settings}
}

// Vendor returns the server vendor this manager handles.
func (m *BaseManager) Vendor() system.Vendor { return m.vendor }

// Capture returns a State snapshot populated from the critical settings map.
func (m *BaseManager) Capture(_ context.Context) (*State, error) {
	state := &State{
		Vendor:   m.vendor,
		Settings: make(map[string]Setting, len(m.settings)),
	}
	for name, val := range m.settings {
		state.Settings[name] = Setting{
			Name:         name,
			CurrentValue: val,
			Type:         "enum",
		}
	}
	m.log.Info("captured BIOS settings", "vendor", m.vendor, "count", len(state.Settings))
	return state, nil
}

// Apply logs each change and returns all names as requiring reboot.
func (m *BaseManager) Apply(_ context.Context, changes []SettingChange) ([]string, error) {
	reboot := make([]string, 0, len(changes))
	for _, c := range changes {
		m.log.Info("apply BIOS setting", "vendor", m.vendor, "name", c.Name, "value", c.Value)
		reboot = append(reboot, c.Name)
	}
	return reboot, nil
}

// Reset is not implemented for the generic base manager.
func (m *BaseManager) Reset(_ context.Context) error {
	return ErrNotImplemented
}
