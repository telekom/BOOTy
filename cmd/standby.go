package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var standbyCmd = &cobra.Command{
	Use:   "standby",
	Short: "Enter hot-standby mode (heartbeat + command polling)",
	Long: `Keeps the machine warm in the ramdisk. Sends periodic heartbeats
to the CAPRF server and polls for commands. When a provision/deprovision/reboot
command arrives, it executes immediately without a full PXE cycle.

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
