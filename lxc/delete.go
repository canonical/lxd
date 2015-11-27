package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
)

type deleteCmd struct{}

func (c *deleteCmd) showByDefault() bool {
	return true
}

func (c *deleteCmd) usage() string {
	return i18n.G(
		`Delete containers or container snapshots.

lxc delete [remote:]<container>[/<snapshot>] [remote:][<container>[/<snapshot>]...]

Destroy containers or snapshots with any attached data (configuration, snapshots, ...).`)
}

func (c *deleteCmd) flags() {}

func doDelete(d *lxd.Client, name string) error {
	resp, err := d.Delete(name)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}

func (c *deleteCmd) run(config *lxd.Config, args []string) error {
	if len(args) == 0 {
		return errArgs
	}

	for _, nameArg := range args {
		remote, name := config.ParseRemoteAndContainer(nameArg)

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		ct, err := d.ContainerStatus(name)

		if err != nil {
			// Could be a snapshot
			return doDelete(d, name)
		}

		if ct.Status.StatusCode != 0 && ct.Status.StatusCode != shared.Stopped {
			resp, err := d.Action(name, shared.Stop, -1, true)
			if err != nil {
				return err
			}

			op, err := d.WaitFor(resp.Operation)
			if err != nil {
				return err
			}

			if op.StatusCode == shared.Failure {
				return fmt.Errorf(i18n.G("Stopping container failed!"))
			}

			if ct.Ephemeral == true {
				return nil
			}
		}
		if err := doDelete(d, name); err != nil {
			return err
		}
	}
	return nil

}
