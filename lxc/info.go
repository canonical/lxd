package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type infoCmd struct{}

func (c *infoCmd) showByDefault() bool {
	return true
}

func (c *infoCmd) usage() string {
	return gettext.Gettext(
		"List information on containers.\n" +
			"\n" +
			"This will support remotes and images as well, but only containers for now.\n" +
			"\n" +
			"lxc info [<remote>:]container\n")
}

func (c *infoCmd) flags() {}

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
	ct, err := d.ContainerStatus(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Name: %s\n", ct.Name)
	fmt.Printf("Status: %s\n", ct.Status.State)
	return nil
}
