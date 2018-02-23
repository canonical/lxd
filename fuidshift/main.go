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
	app.Use = "fuidshift"
	app.Short = "UID/GID shifter"
	app.Long = `Description:
  UID/GID shifter

  This tool lets you remap a filesystem tree, switching it from one
  set of UID/GID ranges to another.

  This is mostly useful when retrieving a wrongly shifted filesystem tree
  from a backup or broken system and having to remap everything either to
  the host UID/GID range (uid/gid 0 is root) or to an existing container's
  range.


  A range is represented as <u|b|g>:<first_container_id>:<first_host_id>:<size>.
  Where "u" means shift uid, "g" means shift gid and "b" means shift uid and gid.
`
	app.Example = `  fuidshift my-dir/ b:0:100000:65536 u:10000:1000:1`
	app.SilenceUsage = true

	// Global flags
	globalCmd := cmdGlobal{}
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// shift command (main)
	shiftCmd := cmdShift{global: &globalCmd}
	app.Flags().BoolVarP(&shiftCmd.flagTestMode, "test", "t", false, "Test mode (no change to files)")
	app.Flags().BoolVarP(&shiftCmd.flagReverse, "reverse", "r", false, "Perform a reverse mapping")
	app.Use = "fuidshift <directory> <range> [<range>...]"
	app.RunE = shiftCmd.Run
	app.Args = cobra.ArbitraryArgs

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
