package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"gopkg.in/lxc/go-lxc.v2"
)

type cmdForkmigrate struct {
	global *cmdGlobal
}

func (c *cmdForkmigrate) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkmigrate <container name> <containers path> <config> <images path> <preserve>"
	cmd.Short = "Restore the container from saved state"
	cmd.Long = `Description:
  Restore the container from saved state

  This internal command is used to start the container as a separate
  process, restoring its recorded state.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkmigrate) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) != 5 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	name := args[0]
	lxcpath := args[1]
	configPath := args[2]
	imagesDir := args[3]

	preservesInodes, err := strconv.ParseBool(args[4])
	if err != nil {
		return err
	}

	d, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return err
	}

	if err := d.LoadConfigFile(configPath); err != nil {
		return err
	}

	/* see https://github.com/golang/go/issues/13155, startContainer, and dc3a229 */
	os.Stdin.Close()
	os.Stdout.Close()
	os.Stderr.Close()

	return d.Migrate(lxc.MIGRATE_RESTORE, lxc.MigrateOptions{
		Directory:       imagesDir,
		Verbose:         true,
		PreservesInodes: preservesInodes,
	})
}
