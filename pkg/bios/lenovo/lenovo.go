package lenovo

import (
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

// criticalSettings contains Lenovo-recommended BIOS settings.
var criticalSettings = map[string]string{
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
	bios.RegisterManager(system.VendorLenovo, func(log *slog.Logger) bios.Manager {
		return bios.NewBaseManager(system.VendorLenovo, log, criticalSettings)
	})
}
