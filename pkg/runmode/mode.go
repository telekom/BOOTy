//go:build linux

package runmode

import (
	"context"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/network"
)

// Mode is the interface for a BOOTy operating mode.
type Mode interface {
	Name() string
	Run(ctx context.Context) error
}

// Deps holds shared dependencies injected into all mode implementations.
type Deps struct {
	Cfg     *config.MachineConfig
	Client  config.Provider
	DiskMgr *disk.Manager
	NetMode network.Mode
}
