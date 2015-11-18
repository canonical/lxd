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
	visible    bool
	name       string
}

func (c *actionCmd) showByDefault() bool {
	return c.visible
}

var timeout = -1
var force = false

func (c *actionCmd) usage() string {
	return fmt.Sprintf(gettext.Gettext(
		`Changes state of one or more containers to %s.

lxc %s <name> [<name>...]`), c.name, c.name)
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
			return fmt.Errorf("%s\n"+gettext.Gettext("Try `lxc info --show-log %s` for more info"), err, name)
		}
	}
	return nil
}
