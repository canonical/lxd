package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type deleteCmd struct{}

const deleteUsage = `
lxc delete <resource>

Destroy a resource (e.g. container) and any attached data (configuration,
snapshots, ...).
`

func (c *deleteCmd) usage() string {
	return deleteUsage
}

func (c *deleteCmd) flags() {}

func (c *deleteCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	d, name, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	resp, err := d.Delete(name)
	if err != nil {
		return err
	}

	op, err := d.WaitFor(resp.Operation)
	if err != nil {
		return err
	}

	if op.Result == lxd.Success {
		return nil
	} else {
		return fmt.Errorf("Operation %s", op.Result)
	}
}
