package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type actionCmd struct {
	action     shared.ContainerAction
	hasTimeout bool
	visible    bool
	name       string
	timeout    int
	force      bool
}

func (c *actionCmd) showByDefault() bool {
	return c.visible
}

func (c *actionCmd) usage() string {
	return fmt.Sprintf(i18n.G(
		`Changes state of one or more containers to %s.

lxc %s <name> [<name>...]`), c.name, c.name)
}

func (c *actionCmd) flags() {
	if c.hasTimeout {
		gnuflag.IntVar(&c.timeout, "timeout", -1, i18n.G("Time to wait for the container before killing it."))
		gnuflag.BoolVar(&c.force, "force", false, i18n.G("Force the container to shutdown."))
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

		resp, err := d.Action(name, c.action, c.timeout, c.force)
		if err != nil {
			return err
		}

		if resp.Type != lxd.Async {
			return fmt.Errorf(i18n.G("bad result type from action"))
		}

		if err := d.WaitForSuccess(resp.Operation); err != nil {
			return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, name)
		}
	}
	return nil
}
