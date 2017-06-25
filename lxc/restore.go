package main

import (
	"fmt"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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
		`Usage: lxc restore [<remote>:]<container> <snapshot> [--stateful]

Restore containers from snapshots.

If --stateful is passed, then the running state will be restored too.

*Examples*
lxc snapshot u1 snap0
    Create the snapshot.

lxc restore u1 snap0
    Restore the snapshot.`)
}

func (c *restoreCmd) flags() {
	gnuflag.BoolVar(&c.stateful, "stateful", false, i18n.G("Whether or not to restore the container's running state from snapshot (if available)"))
}

func (c *restoreCmd) run(conf *config.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	var snapname = args[1]

	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	if !shared.IsSnapshot(snapname) {
		snapname = fmt.Sprintf("%s/%s", name, snapname)
	}

	req := api.ContainerPut{
		Restore:  snapname,
		Stateful: c.stateful,
	}

	op, err := d.UpdateContainer(name, req, "")
	if err != nil {
		return err
	}

	return op.Wait()
}
