package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type actionCmd struct {
	action     lxd.ContainerAction
	hasTimeout bool
}

var timeout = -1
var force = false

func (c *actionCmd) usage() string {
	return fmt.Sprintf(gettext.Gettext(
		"Changes a containers state to %s.\n"+
			"\n"+
			"lxd %s <name>\n"), c.action, c.action)
}

func (c *actionCmd) flags() {
	if c.hasTimeout {
		gnuflag.IntVar(&timeout, "timeout", -1, gettext.Gettext("Time to wait for the container before killing it."))
		gnuflag.BoolVar(&force, "force", false, gettext.Gettext("Force the container to shutdown."))
	}
}

func (c *actionCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	_, err = d.Action(name, c.action, timeout, force)
	return err
}
