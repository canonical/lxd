package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/idmap"
)

type cmdShift struct {
	global *cmdGlobal

	flagReverse  bool
	flagTestMode bool
}

func (c *cmdShift) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "fuidshift <directory> <range> [<range>...]"
	cmd.Short = "UID/GID shifter"
	cmd.Long = `Description:
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
	cmd.Example = `  fuidshift my-dir/ b:0:100000:65536 u:10000:1000:1`
	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagTestMode, "test", "t", false, "Test mode (no change to files)")
	cmd.Flags().BoolVarP(&c.flagReverse, "reverse", "r", false, "Perform a reverse mapping")

	return cmd
}

func (c *cmdShift) Run(cmd *cobra.Command, args []string) error {
	// Help and usage
	if len(args) == 0 {
		return cmd.Help()
	}

	// Sanity checks
	if !c.flagTestMode && os.Geteuid() != 0 {
		return fmt.Errorf("This tool must be run as root")
	}

	// Handle mandatory arguments
	if len(args) < 2 {
		cmd.Help()
		return fmt.Errorf("Missing required arguments")
	}
	directory := args[0]

	// Parse the maps
	idmapSet := idmap.IdmapSet{}
	for _, arg := range args[1:] {
		var err error
		idmapSet, err = idmapSet.Append(arg)
		if err != nil {
			return err
		}
	}

	// Reverse shifting
	if c.flagReverse {
		err := idmapSet.UidshiftFromContainer(directory, c.flagTestMode)
		if err != nil {
			return err
		}

		return nil
	}

	// Normal shifting
	err := idmapSet.UidshiftIntoContainer(directory, c.flagTestMode)
	if err != nil {
		return err
	}

	return nil
}
