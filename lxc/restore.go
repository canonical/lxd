package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type restoreCmd struct {
	stateful bool
}

func (c *restoreCmd) showByDefault() bool {
	return true
}

func (c *restoreCmd) usage() string {
	return i18n.G(
		`Restore a container's state to a previous snapshot.

lxc restore [<remote>:]<container> <snapshot> [--stateful]

Restores a container from a snapshot (optionally with running state, see
snapshot help for details).

Examples:
Create the snapshot:
    lxc snapshot u1 snap0

Restore the snapshot:
    lxc restore u1 snap0`)
}

func (c *restoreCmd) flags() {
	gnuflag.BoolVar(&c.stateful, "stateful", false, i18n.G("Whether or not to restore the container's running state from snapshot (if available)"))
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
