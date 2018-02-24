package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/version"
)

type cmdGlobal struct {
	flagVersion bool
	flagHelp    bool
}

func main() {
	app := &cobra.Command{}
	app.Short = "Physical to container migration tool"
	app.Long = `Description:
  Physical to container migration tool

  This tool lets you turn any Linux filesystem (including your current one)
  into a LXD container on a remote LXD host.

  It will setup a clean mount tree made of the root filesystem and any
  additional mount you list, then transfer this through LXD's migration
  API to create a new container from it.

  The same set of options as ` + "`lxc launch`" + ` are also supported.
`
	app.SilenceUsage = true

	// Global flags
	globalCmd := cmdGlobal{}
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// migrate command (main)
	migrateCmd := cmdMigrate{global: &globalCmd}
	app.Flags().StringArrayVarP(&migrateCmd.flagConfig, "config", "c", nil, "Configuration key and value to set on the container"+"``")
	app.Flags().StringVarP(&migrateCmd.flagNetwork, "network", "n", "", "Network to use for the container"+"``")
	app.Flags().StringArrayVarP(&migrateCmd.flagProfile, "profile", "p", nil, "Profile to apply to the container"+"``")
	app.Flags().StringVarP(&migrateCmd.flagStorage, "storage", "s", "", "Storage pool to use for the container"+"``")
	app.Flags().StringVarP(&migrateCmd.flagType, "type", "t", "", "Instance type to use for the container"+"``")
	app.Flags().BoolVar(&migrateCmd.flagNoProfiles, "no-profiles", false, "Create the container with no profiles applied")
	app.Use = "lxd-p2c <target URL> <container name> <filesystem root> [<filesystem mounts>...]"
	app.RunE = migrateCmd.Run
	app.Args = cobra.ArbitraryArgs

	// netcat sub-command
	netcatCmd := cmdNetcat{global: &globalCmd}
	appNetcat := &cobra.Command{}
	appNetcat.Use = "netcat <address>"
	appNetcat.Hidden = true
	appNetcat.Short = "Sends stdin data to a unix socket"
	appNetcat.RunE = netcatCmd.Run
	app.AddCommand(appNetcat)

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
