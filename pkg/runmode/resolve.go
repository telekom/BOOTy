//go:build linux

package runmode

import (
	"fmt"

	"github.com/telekom/BOOTy/pkg/config"
)

// Resolve returns the Mode implementation matching the config's Mode field.
func Resolve(deps Deps) (Mode, error) {
	mode := deps.Cfg.Mode
	switch {
	case mode == "standby":
		return &StandbyMode{deps: deps}, nil
	case mode == "dry-run":
		return &DryRunMode{deps: deps}, nil
	case config.IsDeprovisionMode(mode):
		return &DeprovisionMode{deps: deps}, nil
	case mode == "check":
		return &CheckMode{deps: deps}, nil
	case mode == "provision" || mode == "":
		return &ProvisionMode{deps: deps}, nil
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
}
