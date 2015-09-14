package main

import (
	"fmt"

	"github.com/chai2010/gettext-go/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

type actionCmd struct {
	action     shared.ContainerAction
	hasTimeout bool
}

func (c *actionCmd) showByDefault() bool {
	return true
}

var timeout = -1
var force = false

func (c *actionCmd) usage() string {
	return fmt.Sprintf(gettext.Gettext(
		`Changes one or more containers state to %s.

lxc %s <name> [<name>...]`), c.action, c.action)
}

func (c *actionCmd) flags() {
	if c.hasTimeout {
		gnuflag.IntVar(&timeout, "timeout", -1, gettext.Gettext("Time to wait for the container before killing it."))
		gnuflag.BoolVar(&force, "force", false, gettext.Gettext("Force the container to shutdown."))
	}
}

func (c *actionCmd) run(config *lxd.Config, args []string) error {
	if len(args) == 0 {
		return errArgs
	}

	for _, nameArg := range args {
		remote, name := config.ParseRemoteAndContainer(nameArg)
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		resp, err := d.Action(name, c.action, timeout, force)
		if err != nil {
			return err
		}

		if resp.Type != lxd.Async {
			return fmt.Errorf(gettext.Gettext("bad result type from action"))
		}

		if err := d.WaitForSuccess(resp.Operation); err != nil {
			return err
		}
	}
	return nil
}
