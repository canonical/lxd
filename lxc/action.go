package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type actionCmd struct {
	action lxd.ContainerAction
}

func (c *actionCmd) usage() string {
	return fmt.Sprintf(`
Creates a container using the specified release and arch

lxd %s <name>
`, c.action)
}

func (c *actionCmd) flags() {}

func (c *actionCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	d, name, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	// TODO: implement --force and --timeout where necessary
	_, err = d.Action(name, c.action, -1, false)
	return err
}
