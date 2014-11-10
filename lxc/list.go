package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type listCmd struct{}

const listUsage = `
Lists the available resources.

lxc list [resource]

Currently resource must be a defined remote, and list only lists
the defined containers.
`

func (c *listCmd) usage() string {
	return listUsage
}

func (c *listCmd) flags() {}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		remote = args[0]
	} else {
		remote = config.DefaultRemote
	}

	d, _, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	l, err := d.List()
	if err != nil {
		return err
	}
	fmt.Println(l)
	return err
}
