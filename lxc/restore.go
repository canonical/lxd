package main

import (
	"fmt"

	"github.com/codegangsta/cli"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandRestore = cli.Command{
	Name:      "restore",
	Usage:     i18n.G("Set the current state of a resource back to what it was when it was snapshotted."),
	ArgsUsage: i18n.G("[remote:]<resource> <snapshot name> [--stateful]"),
	Description: i18n.G(`Set the current state of a resource back to a snapshot.

   lxc restore [remote:]<container> <snapshot name> [--stateful]

   Restores a container from a snapshot (optionally with running state, see
   snapshot help for details).

   For example:
   lxc snapshot u1 snap0 # create the snapshot
   lxc restore u1 snap0 # restore the snapshot`),

	Flags: append(commandGlobalFlags,
		cli.BoolFlag{
			Name:  "stateful",
			Usage: i18n.G("Whether or not to restore the container's running state from snapshot (if available)."),
		},
	),
	Action: commandWrapper(commmandActionRestore),
}

func commmandActionRestore(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var stateful = context.Bool("stateful")

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

	resp, err := d.RestoreSnapshot(name, snapname, stateful)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
