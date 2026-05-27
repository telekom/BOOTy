package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/telekom/BOOTy/pkg/config"
)

var validateStrict bool

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a configuration file",
	Long: `Loads and validates the configuration file without executing any
operations. Reports all validation errors found.

By default, unknown fields in YAML/JSON are silently ignored. Use --strict
to reject them (recommended for CI pipelines).

Exit code 0 if valid, non-zero with error details if invalid.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			return fmt.Errorf("--config flag is required")
		}
		cfg, err := config.LoadWithOptions(config.LoadOptions{Path: configPath, Strict: validateStrict})
		if err != nil {
			return fmt.Errorf("configuration invalid: %w", err)
		}
		fmt.Printf("Configuration valid (mode=%s, hostname=%s)\n", cfg.Mode, cfg.Hostname)
		return nil
	},
}

func init() {
	validateCmd.Flags().BoolVar(&validateStrict, "strict", false,
		"reject unknown fields in YAML/JSON (recommended for CI)")
}
