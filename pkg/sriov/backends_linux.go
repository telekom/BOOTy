//go:build linux

package sriov

import (
	"log/slog"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

// DefaultBackends returns the firmware backends of the ramdisk, in the
// order BOOTy asks them: mstconfig (mstflint), mlxconfig (MFT, only with
// NIC_TOOLS=vendor and a flavor that has it), devlink parameters and the
// Intel private flags.
func DefaultBackends(log *slog.Logger) []FirmwareBackend {
	cmd := nic.OSCommander{}
	return []FirmwareBackend{
		NewMstconfig(cmd),
		NewMlxconfig(cmd),
		NewDevlinkParams(),
		NewIntelFlags(log, cmd),
	}
}
