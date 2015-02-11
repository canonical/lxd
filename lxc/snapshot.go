package main

import (
	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type snapshotCmd struct {
	stateful bool
}

func (c *snapshotCmd) showByDefault() bool {
	return true
}

func (c *snapshotCmd) usage() string {
	return gettext.Gettext(
		"Create a read-only snapshot of a container.\n" +
			"\n" +
			"lxc snapshot <source> <snapshot name> [--stateful]\n")
}

func (c *snapshotCmd) flags() {
	gnuflag.BoolVar(&c.stateful, "stateful", false, gettext.Gettext("Whether or not to snapshot the container's running state"))
}

func (c *snapshotCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	resp, err := d.Snapshot(name, args[1], c.stateful)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
