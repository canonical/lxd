package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdRestore struct {
	global *cmdGlobal

	flagStateful bool
}

// Command returns a Cobra command for the "restore" subcommand, used to restore instances from snapshots, optionally restoring their running state as well.
func (c *cmdRestore) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("restore", i18n.G("[<remote>:]<instance> <snapshot>"))
	cmd.Short = i18n.G("Restore instances from snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Restore instances from snapshots

If --stateful is passed, then the running state will be restored too.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc snapshot u1 snap0
    Create the snapshot.

lxc restore u1 snap0
    Restore the snapshot.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to restore the instance's running state from snapshot (if available)"))

	return cmd
}

// Run restores an instance from a snapshot, optionally restoring its running state as well.
func (c *cmdRestore) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Connect to LXD
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Setup the snapshot restore
	snapname := args[1]
	if !shared.IsSnapshot(snapname) {
		snapname = fmt.Sprintf("%s/%s", name, snapname)
	}

	req := api.InstancePut{
		Restore:  snapname,
		Stateful: c.flagStateful,
	}

	// Restore the snapshot
	op, err := d.UpdateInstance(name, req, "")
	if err != nil {
		return err
	}

	return op.Wait()
}
