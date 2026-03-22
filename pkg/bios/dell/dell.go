package dell

import (
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

// criticalSettings contains Dell-recommended BIOS settings.
var criticalSettings = map[string]string{
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
		return bios.NewBaseManager(system.VendorDell, log, criticalSettings)
	})
}
