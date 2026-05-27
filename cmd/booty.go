package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Release - this struct contains the release information populated when building booty.
var Release struct {
	Version string
	Build   string
}

var configPath string

var bootyCmd = &cobra.Command{
	Use:   "booty",
	Short: "Bare-metal provisioning agent for data center servers",
	Long: `BOOTy (Bare-metal OS Orchestration Tool) manages the full lifecycle
of physical servers: provisioning, deprovisioning, health checking, and
hot-standby agent mode.

Configuration is loaded from a single source file. Format is auto-detected
from the file extension (.yaml, .yml, or .json).`,
}

func init() {
	bootyCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "",
		"path to configuration file (YAML/JSON); auto-detects format from extension")

	bootyCmd.AddCommand(
		bootyVersion,
		provisionCmd,
		deprovisionCmd,
		standbyCmd,
		checkCmd,
		validateCmd,
	)
}

// Execute starts the command parsing process.
func Execute() {
	if err := bootyCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var bootyVersion = &cobra.Command{
	Use:   "version",
	Short: "Version and Release information about the BOOTy image manager",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("BOOTy Release Information\n")
		fmt.Printf("Version:  %s\n", Release.Version)
		fmt.Printf("Build:    %s\n", Release.Build)
	},
}
