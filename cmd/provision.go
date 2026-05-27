package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/telekom/BOOTy/pkg/config"
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a bare-metal server with an OS image",
	Long: `Runs the full provisioning pipeline: disk detection, image streaming,
partitioning, OS configuration, cloud-init injection, and kexec/reboot.

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

func loadConfig() (*config.MachineConfig, error) {
	if configPath == "" {
		return nil, fmt.Errorf("--config flag is required")
	}
	return config.Load(configPath)
}
