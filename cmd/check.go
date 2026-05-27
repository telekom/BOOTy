package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run hardware health checks",
	Long: `Validates configuration for health checking.

Currently validates and reports readiness. Full execution (CPU, memory,
disk, thermal, NIC link checks) will be wired in a future release.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		cfg.Mode = "check"
		fmt.Printf("check mode ready (hostname=%s)\n", cfg.Hostname)
		return nil
	},
}
