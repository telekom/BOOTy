package hpe

import (
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
		return bios.NewBaseManager(system.VendorHPE, log, criticalSettings)
	})
}
