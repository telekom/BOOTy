package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a bare-metal server with an OS image",
	Long: `Validates configuration for the provisioning pipeline.

Currently validates and reports readiness. Full execution (disk detection,
image streaming, partitioning, kexec/reboot) will be wired in a future release.

Requires a configuration file with at minimum the image URL and transport
endpoints configured.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		cfg.Mode = "provision"
		fmt.Printf("provision mode ready (hostname=%s)\n", cfg.Hostname)
		return nil
	},
}
