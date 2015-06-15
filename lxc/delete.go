package main

import (
	"fmt"

	"github.com/gosexy/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type deleteCmd struct{}

func (c *deleteCmd) showByDefault() bool {
	return true
}

func (c *deleteCmd) usage() string {
	return gettext.Gettext(
		"Delete a container or container snapshot.\n" +
			"\n" +
			"lxc delete <container>[/<snapshot>]\n" +
			"\n" +
			"Destroy a container or snapshot with any attached data (configuration,\n" +
			"snapshots, ...).\n")
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
	if len(args) != 1 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	ct, err := d.ContainerStatus(name, false)

	if err != nil {
		// Could be a snapshot
		return doDelete(d, name)
	}

	if ct.State() != shared.STOPPED {
		resp, err := d.Action(name, shared.Stop, -1, true)
		if err != nil {
			return err
		}

		op, err := d.WaitFor(resp.Operation)
		if err != nil {
			return err
		}

		if op.StatusCode == shared.Failure {
			return fmt.Errorf(gettext.Gettext("Stopping container failed!"))
		}

		if ct.Ephemeral == true {
			return nil
		}
	}

	return doDelete(d, name)
}
