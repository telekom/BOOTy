package cmd

import (
	"fmt"

	"github.com/telekom/BOOTy/pkg/config"
)

// loadConfig loads and validates the config file from the --config flag.
func loadConfig() (*config.MachineConfig, error) {
	if configPath == "" {
		return nil, fmt.Errorf("--config flag is required")
	}
	return config.Load(configPath)
}
