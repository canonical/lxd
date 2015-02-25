package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	//	"github.com/olekukonko/tablewriter"
)

type listCmd struct{}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return gettext.Gettext(
		"Lists the available resources.\n" +
			"\n" +
			"lxc list [resource]\n" +
			"\n" +
			"Currently resource must be a defined remote, and list only lists\n" +
			"the defined containers.\n")

}

func (c *listCmd) flags() {}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		remote = config.ParseRemote(args[0])
	} else {
		remote = config.DefaultRemote
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	cts, err := d.ListContainers()
	if err != nil {
		return err
	}

	for _, ct := range cts {
		// get more information
		c, err := d.ContainerStatus(ct)
		if err == nil {
			fmt.Printf("%s: %s\n", ct, c.Status.State)
		} else {
			fmt.Printf("%s: Unknown\n", ct)
		}
	}

	// List snapshots
	found_snapshots := false
	fmt.Printf("\nSnapshots:")
	for _, ct := range cts {
		// get more information
		snaps, err := d.ListSnapshots(ct)
		if err != nil {
			continue
		}
		for _, snap := range snaps {
			fmt.Printf("\n  %s: %s", ct, snap)
			found_snapshots = true
		}
	}

	if !found_snapshots {
		fmt.Printf(" (none)")
	}
	fmt.Printf("\n")
	return nil
}
