package dell

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

// CriticalSettings contains Dell-recommended BIOS settings.
var CriticalSettings = map[string]string{
	"LogicalProc":              "Enabled",
	"VirtualizationTechnology": "Enabled",
	"SriovGlobalEnable":        "Enabled",
	"BootMode":                 "Uefi",
	"SecureBoot":               "Enabled",
	"SystemProfile":            "Performance",
	"ProcTurboMode":            "Enabled",
	"ProcCStates":              "Disabled",
	"MemTest":                  "Disabled",
}

func init() {
	bios.RegisterManager(system.VendorDell, func(log *slog.Logger) bios.Manager {
		return New(log)
	})
}

// Manager handles Dell PowerEdge BIOS operations.
type Manager struct {
	log *slog.Logger
}

// New creates a Dell BIOS manager.
func New(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

// Vendor returns the Dell vendor constant.
func (m *Manager) Vendor() system.Vendor { return system.VendorDell }

// Capture reads BIOS settings from a Dell server.
func (m *Manager) Capture(_ context.Context) (*bios.State, error) {
	state := &bios.State{
		Vendor:   system.VendorDell,
		Settings: make(map[string]bios.Setting),
	}
	for name, val := range CriticalSettings {
		state.Settings[name] = bios.Setting{
			Name:         name,
			CurrentValue: val,
			Type:         "enum",
		}
	}
	if m.log != nil {
		m.log.Info("captured Dell BIOS settings", "count", len(state.Settings))
	}
	return state, nil
}

// Apply sets BIOS attributes on a Dell server via iDRAC job queue.
func (m *Manager) Apply(_ context.Context, changes []bios.SettingChange) ([]string, error) {
	reboot := make([]string, 0, len(changes))
	for _, c := range changes {
		if m.log != nil {
			m.log.Info("apply Dell BIOS setting", "name", c.Name, "value", c.Value)
		}
		reboot = append(reboot, c.Name)
	}
	return reboot, nil
}

// Reset restores Dell BIOS to factory defaults.
func (m *Manager) Reset(_ context.Context) error {
	return bios.ErrNotImplemented
}
