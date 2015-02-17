package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"
)

type deleteCmd struct{}

func (c *deleteCmd) showByDefault() bool {
	return true
}

func (c *deleteCmd) usage() string {
	return gettext.Gettext(
		"lxc delete <resource>\n" +
			"\n" +
			"Destroy a resource (e.g. container) and any attached data (configuration,\n" +
			"snapshots, ...).\n")
}

func (c *deleteCmd) flags() {}

func (c *deleteCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	ct, err := d.ContainerStatus(name)

	if err != nil {
		return err
	}

	if ct.State() != lxc.STOPPED {
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
	}

	resp, err := d.Delete(name)
	if err != nil {
		return err
	}

	op, err := d.WaitFor(resp.Operation)
	if err != nil {
		return err
	}

	if op.StatusCode == shared.Success {
		return nil
	} else {
		return fmt.Errorf(gettext.Gettext("Operation %s"), op.Status)
	}
}
