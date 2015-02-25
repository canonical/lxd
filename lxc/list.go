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

func listContainers(d *lxd.Client) error {
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
	return nil
}

func listContainer(d *lxd.Client, name string) error {
	status, err := d.ContainerStatus(name)
	if err != nil {
		return err
	}
	fmt.Printf("Name: %s\n", name)
	fmt.Printf("State: %s\n", status.Status.State)

	// List snapshots
	first_snapshot := true
	snaps, err := d.ListSnapshots(name)
	if err != nil {
		return nil
	}
	for _, snap := range snaps {
		if first_snapshot {
			fmt.Printf("Snapshots:\n")
		}
		fmt.Printf("  %s\n", snap)
		first_snapshot = false
	}
	return nil
}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	var name string
	if len(args) == 1 {
		remote, name = config.ParseRemoteAndContainer(args[0])
	} else {
		remote = config.DefaultRemote
		name = ""
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if name == "" {
		return listContainers(d)
	}

	return listContainer(d, name)
}
