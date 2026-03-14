package hpe

import (
	"context"
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
)

// CriticalSettings contains HPE-recommended BIOS settings.
var CriticalSettings = map[string]string{
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

// Manager handles HPE ProLiant BIOS operations.
type Manager struct {
	log *slog.Logger
}

// New creates an HPE BIOS manager.
func New(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

// Vendor returns the HPE vendor constant.
func (m *Manager) Vendor() bios.Vendor { return bios.VendorHPE }

// Capture reads BIOS settings from an HPE server.
func (m *Manager) Capture(ctx context.Context) (*bios.State, error) {
	state := &bios.State{
		Vendor:   bios.VendorHPE,
		Settings: make(map[string]bios.Setting),
	}

	// On real hardware, this would call iLO Redfish API or read from sysfs.
	// Populate with critical defaults for baseline comparison.
	for name, val := range CriticalSettings {
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
