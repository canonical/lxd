package main

import (
	"fmt"

	"github.com/chai2010/gettext-go/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
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
			"lxc snapshot [remote:]<source> <snapshot name> [--stateful]\n")
}

func (c *snapshotCmd) flags() {
	gnuflag.BoolVar(&c.stateful, "stateful", false, gettext.Gettext("Whether or not to snapshot the container's running state"))
}

func (c *snapshotCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	var snapname string
	if len(args) < 2 {
		snapname = ""
	} else {
		snapname = args[1]
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	// we don't allow '/' in snapshot names
	if shared.IsSnapshot(snapname) {
		return fmt.Errorf(gettext.Gettext("'/' not allowed in snapshot name\n"))
	}

	resp, err := d.Snapshot(name, snapname, c.stateful)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
