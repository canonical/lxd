package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type infoCmd struct {
	showLog bool
}

func (c *infoCmd) showByDefault() bool {
	return true
}

func (c *infoCmd) usage() string {
	return gettext.Gettext(
		"List information on containers.\n" +
			"\n" +
			"This will support remotes and images as well, but only containers for now.\n" +
			"\n" +
			"lxc info [<remote>:]container [--show-log]\n")
}

func (c *infoCmd) flags() {
	gnuflag.BoolVar(&c.showLog, "show-log", false, gettext.Gettext("Show the container's last 100 log lines?"))
}

func (c *infoCmd) run(config *lxd.Config, args []string) error {
	var remote string
	var cName string
	if len(args) == 1 {
		remote, cName = config.ParseRemoteAndContainer(args[0])
	} else {
		remote = config.DefaultRemote
		cName = ""
	}
	if cName == "" {
		fmt.Printf(gettext.Gettext("Information about remotes not yet supported\n"))
		return errArgs
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	ct, err := d.ContainerStatus(cName, c.showLog)
	if err != nil {
		return err
	}
	fmt.Printf("Name: %s\n", ct.Name)
	fmt.Printf("Status: %s\n", ct.Status.State)
	if ct.Status.Init != 0 {
		fmt.Printf("Init: %d\n", ct.Status.Init)
		fmt.Printf("Ips:\n")
		foundone := false
		for _, ip := range ct.Status.Ips {
			fmt.Printf("  %s:\t %s\t%s\n", ip.Interface, ip.Protocol, ip.Address)
			foundone = true
		}
		if !foundone {
			fmt.Printf("(none)\n")
		}
	}

	// List snapshots
	first_snapshot := true
	snaps, err := d.ListSnapshots(cName)
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

	if c.showLog {
		fmt.Printf("\nLog:\n\n%s\n", ct.Log)
	}

	return nil
}
