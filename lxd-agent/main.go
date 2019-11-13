package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/version"
)

type cmdGlobal struct {
	flagVersion bool
	flagHelp    bool

	flagLogDebug bool
}

func main() {
	// agent command (main)
	agentCmd := cmdAgent{}
	app := agentCmd.Command()
	app.SilenceUsage = true

	// Workaround for main command
	app.Args = cobra.ArbitraryArgs

	// Global flags
	globalCmd := cmdGlobal{}
	agentCmd.global = &globalCmd
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogDebug, "debug", "d", false, "Show all debug messages")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
