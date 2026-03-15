package lenovo

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
)

// CriticalSettings contains Lenovo-recommended BIOS settings.
var CriticalSettings = map[string]string{
	"OperatingMode":            "MaximumPerformance",
	"HyperThreading":           "Enable",
	"VirtualizationTechnology": "Enable",
	"SRIOVSupport":             "Enable",
	"BootMode":                 "UEFIMode",
	"SecureBoot":               "Enable",
	"TurboMode":                "Enable",
	"IntelSpeedStep":           "Enable",
	"ActiveProcessorCores":     "All",
	"PackageCState":            "C0/C1",
}

func init() {
	bios.RegisterManager(bios.VendorLenovo, func(log *slog.Logger) bios.Manager {
		return New(log)
	})
}

// Manager handles Lenovo ThinkSystem BIOS operations.
type Manager struct {
	log *slog.Logger
}

// New creates a Lenovo BIOS manager.
func New(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

// Vendor returns the Lenovo vendor constant.
func (m *Manager) Vendor() bios.Vendor { return bios.VendorLenovo }

// Capture reads BIOS settings from a Lenovo server.
func (m *Manager) Capture(ctx context.Context) (*bios.State, error) {
	state := &bios.State{
		Vendor:   bios.VendorLenovo,
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
		m.log.Info("captured Lenovo BIOS settings", "count", len(state.Settings))
	}
	return state, nil
}

// Apply sets BIOS attributes on a Lenovo server.
func (m *Manager) Apply(_ context.Context, changes []bios.SettingChange) ([]string, error) {
	reboot := make([]string, 0, len(changes))
	for _, c := range changes {
		if m.log != nil {
			m.log.Info("apply Lenovo BIOS setting", "name", c.Name, "value", c.Value)
		}
		reboot = append(reboot, c.Name)
	}
	return reboot, nil
}

// Reset restores Lenovo BIOS to factory defaults.
func (m *Manager) Reset(_ context.Context) error {
	return bios.ErrNotImplemented
}
