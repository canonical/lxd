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
