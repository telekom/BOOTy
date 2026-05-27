package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var standbyCmd = &cobra.Command{
	Use:   "standby",
	Short: "Enter hot-standby mode (heartbeat + command polling)",
	Long: `Validates configuration for hot-standby mode.

Currently validates and reports readiness. Full execution (heartbeat loop,
command polling) will be wired in a future release.

Requires agent.heartbeatURL and agent.commandsURL to be configured.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		cfg.Mode = "standby"
		fmt.Printf("standby mode ready (hostname=%s)\n", cfg.Hostname)
		return nil
	},
}
