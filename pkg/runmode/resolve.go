//go:build linux

package runmode

import "fmt"

// Resolve returns the Mode implementation matching the config's Mode field.
func Resolve(deps Deps) (Mode, error) {
	switch deps.Cfg.Mode {
	case "standby":
		return &StandbyMode{deps: deps}, nil
	case "dry-run":
		return &DryRunMode{deps: deps}, nil
	case "deprovision", "soft-deprovision", "soft", "hard":
		return &DeprovisionMode{deps: deps}, nil
	case "check":
		return &CheckMode{deps: deps}, nil
	case "provision", "":
		return &ProvisionMode{deps: deps}, nil
	default:
		return nil, fmt.Errorf("unknown mode %q", deps.Cfg.Mode)
	}
}
