package main

import (
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdRestore struct {
	global *cmdGlobal

	flagStateful     bool
	flagDisks        string
	flagExcludeDisks string
}

func (c *cmdRestore) command() *cobra.Command {
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

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to restore the instance's running state from snapshot (if available)"))
	cmd.Flags().StringVar(&c.flagDisks, "disks", "", i18n.G(`Which disks should be included when restoring; can be "root" or "volumes"`))
	cmd.Flags().StringVar(&c.flagExcludeDisks, "exclude-disks", "", i18n.G(`Comma-separated list of names for which disks should be ignored when performing a multi-volume restore`))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpTopLevelResource("instance", toComplete)
		}

		remote, instanceName, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpSnapshotNames(remote, instanceName, toComplete)
	}

	return cmd
}

func (c *cmdRestore) run(cmd *cobra.Command, args []string) error {
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
		snapname = name + "/" + snapname
	}

	req := api.InstancePut{
		Restore:      snapname,
		Stateful:     c.flagStateful,
		DisksMode:    c.flagDisks,
		ExcludeDisks: shared.SplitNTrimSpace(c.flagExcludeDisks, ",", -1, true),
	}

	// Restore the snapshot
	op, err := d.UpdateInstance(name, req, "")
	if err != nil {
		return err
	}

	return op.Wait()
}
