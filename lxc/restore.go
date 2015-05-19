package main

import (
	"github.com/gosexy/gettext"

	"fmt"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
)

type restoreCmd struct {
	stateful bool
}

func (c *restoreCmd) showByDefault() bool {
	return true
}

func (c *restoreCmd) usage() string {
	return gettext.Gettext(
		"Set the current state of a resource back to what it was when it was snapshotted.\n" +
			"\n" +
			"lxc restore <resource> <snapshot name> [--stateful]\n")
}

func (c *restoreCmd) flags() {
	gnuflag.BoolVar(&c.stateful, "stateful", false, gettext.Gettext("Whether or not to restore the container's running state from snapshot (if available)"))
}

func (c *restoreCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	var snapname = args[1]

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if !shared.IsSnapshot(snapname) {
		snapname = fmt.Sprintf("%s/%s", name, snapname)
	}

	resp, err := d.RestoreSnapshot(name, snapname, c.stateful)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
