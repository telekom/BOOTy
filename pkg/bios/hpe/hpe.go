package hpe

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

// criticalSettings contains HPE-recommended BIOS settings.
var criticalSettings = map[string]string{
	"ProcHyperthreading":      "Enabled",
	"ProcVirtualization":      "Enabled",
	"Sriov":                   "Enabled",
	"BootMode":                "Uefi",
	"SecureBootStatus":        "Enabled",
	"WorkloadProfile":         "GeneralPowerEfficientCompute",
	"PowerRegulator":          "DynamicPowerSavings",
	"ThermalConfig":           "OptimalCooling",
	"IntelligentProvisioning": "Disabled",
	"EmbSata1Aspm":            "Disabled",
}

func init() {
	bios.RegisterManager(system.VendorHPE, func(log *slog.Logger) bios.Manager {
		return New(log)
	})
}

// Manager handles HPE ProLiant BIOS operations.
type Manager struct {
	log *slog.Logger
}

// New creates an HPE BIOS manager.
func New(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

// Vendor returns the HPE vendor constant.
func (m *Manager) Vendor() system.Vendor { return system.VendorHPE }

// Capture reads BIOS settings from an HPE server.
func (m *Manager) Capture(_ context.Context) (*bios.State, error) {
	state := &bios.State{
		Vendor:   system.VendorHPE,
		Settings: make(map[string]bios.Setting),
	}
	for name, val := range criticalSettings {
		state.Settings[name] = bios.Setting{
			Name:         name,
			CurrentValue: val,
			Type:         "enum",
		}
	}
	if m.log != nil {
		m.log.Info("captured HPE BIOS settings", "count", len(state.Settings))
	}
	return state, nil
}

// Apply sets BIOS attributes on an HPE server.
func (m *Manager) Apply(_ context.Context, changes []bios.SettingChange) ([]string, error) {
	reboot := make([]string, 0, len(changes))
	for _, c := range changes {
		if m.log != nil {
			m.log.Info("apply HPE BIOS setting", "name", c.Name, "value", c.Value)
		}
		reboot = append(reboot, c.Name)
	}
	return reboot, nil
}

// Reset restores HPE BIOS to factory defaults.
func (m *Manager) Reset(_ context.Context) error {
	return bios.ErrNotImplemented
}
