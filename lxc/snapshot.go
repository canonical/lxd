package main

import (
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type snapshotCmd struct {
	stateful bool
}

const snapshotUsage = `
Create a read-only snapshot of a container.

lxc snapshot <source> <snapshot name> [--stateful]
`

func (c *snapshotCmd) usage() string {
	return snapshotUsage
}

func (c *snapshotCmd) flags() {
	gnuflag.BoolVar(&c.stateful, "stateful", false, "Whether or not to snapshot the container's running state")
}

func (c *snapshotCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	d, name, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	resp, err := d.Snapshot(name, args[1], c.stateful)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
