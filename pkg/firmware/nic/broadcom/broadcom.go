//go:build linux

package broadcom

import (
	"log/slog"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
	"github.com/telekom/BOOTy/pkg/firmware/nic/ethtool"
)

// Manager handles Broadcom NIC firmware operations.
type Manager struct {
	*ethtool.Manager
}

// New creates a Broadcom firmware manager.
func New(log *slog.Logger) *Manager {
	return &Manager{Manager: ethtool.New(nic.VendorBroadcom, "0x14e4", log)}
}

// NewWithCommander creates a Broadcom firmware manager with a custom commander.
func NewWithCommander(log *slog.Logger, commander ethtool.Commander) *Manager {
	return &Manager{Manager: ethtool.NewWithCommander(nic.VendorBroadcom, "0x14e4", log, commander)}
}

// DriverType returns "tg3" or "bnxt_en" based on the NIC driver.
func DriverType(n *nic.Identifier) string {
	if n == nil {
		return "unknown"
	}
	switch n.Driver {
	case "tg3":
		return "tg3"
	case "bnxt_en":
		return "bnxt_en"
	default:
		return "unknown"
	}
}
