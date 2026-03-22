package supermicro

import (
	"log/slog"

	"github.com/telekom/BOOTy/pkg/bios"
	"github.com/telekom/BOOTy/pkg/system"
)

func init() {
	bios.RegisterManager(system.VendorSupermicro, func(log *slog.Logger) bios.Manager {
		return bios.NewBaseManager(system.VendorSupermicro, log, nil)
	})
}
