package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type actionCmd struct {
	action lxd.ContainerAction
}

func (c *actionCmd) usage() string {
	return fmt.Sprintf(gettext.Gettext(
		"Changes a containers state to %s.\n"+
			"\n"+
			"lxd %s <name>\n"), c.action, c.action)
}

func (c *actionCmd) flags() {}

func (c *actionCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	// TODO: implement --force and --timeout where necessary
	_, err = d.Action(name, c.action, -1, false)
	return err
}
