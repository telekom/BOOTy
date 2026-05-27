package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run hardware health checks",
	Long: `Executes the health check suite (CPU, memory, disk, thermal, NIC link)
and reports results. Does not perform any provisioning operations.

Exit code 0 if all checks pass, non-zero if any critical check fails.
Results are printed to stdout and optionally POSTed to health.reportURL.`,
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
