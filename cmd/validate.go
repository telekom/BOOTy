package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/telekom/BOOTy/pkg/config"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a configuration file",
	Long: `Loads and validates the configuration file without executing any
operations. Reports all validation errors found.

Useful for CI pipelines and pre-deployment checks.

Exit code 0 if valid, non-zero with error details if invalid.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			return fmt.Errorf("--config flag is required")
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("configuration invalid: %w", err)
		}
		fmt.Printf("Configuration valid (mode=%s, hostname=%s)\n", cfg.Mode, cfg.Hostname)
		return nil
	},
}
