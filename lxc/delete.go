package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"gopkg.in/lxc/go-lxc.v2"
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

	ct, err := d.ContainerStatus(name)

	if err != nil {
		return err
	}

	if ct.State() == lxc.STARTING || ct.State() == lxc.RUNNING {
		resp, err := d.Action(name, lxd.Stop, -1, true)
		if err != nil {
			return err
		}

		_, err = d.WaitFor(resp.Operation)
		if err != nil {
			return err
		}
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
