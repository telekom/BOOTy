package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deprovisionSoft bool

var deprovisionCmd = &cobra.Command{
	Use:   "deprovision",
	Short: "Wipe or disable the installed OS on a server",
	Long: `Deprovisioning modes:
  - Hard (default): Secure erase or wipefs on all target disks
  - Soft (--soft): Rename grub.cfg to make the OS unbootable without data loss`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if deprovisionSoft {
			cfg.Mode = "soft-deprovision"
		} else {
			cfg.Mode = "deprovision"
		}
		fmt.Printf("deprovision mode ready (hostname=%s, soft=%v)\n", cfg.Hostname, deprovisionSoft)
		return nil
	},
}

func init() {
	deprovisionCmd.Flags().BoolVar(&deprovisionSoft, "soft", false,
		"soft deprovision (rename grub.cfg only, no disk wipe)")
}
