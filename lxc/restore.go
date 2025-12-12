package main

import (
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdRestore struct {
	global *cmdGlobal

	flagStateful    bool
	flagDiskVolumes string
}

func (c *cmdRestore) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("restore", "[<remote>:]<instance> <snapshot>")
	cmd.Short = "Restore instances from snapshots"
	cmd.Long = cli.FormatSection("Description", `Restore instances from snapshots

If --stateful is passed, then the running state will be restored too.`)
	cmd.Example = cli.FormatSection("", `lxc snapshot u1 snap0
    Create the snapshot.

lxc restore u1 snap0
    Restore the snapshot.`)

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, "Whether or not to restore the instance's running state from snapshot (if available)")
	cmd.Flags().StringVar(&c.flagDiskVolumes, "disk-volumes", "", `Disk volumes mode. Possible values are "root" (default) and "all-exclusive". "root" only restores the instance's root disk volume. "all-exclusive" restores the instance's root disk and any exclusively attached volumes (non-shared) snapshots.`)

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
		Restore:                snapname,
		Stateful:               c.flagStateful,
		RestoreDiskVolumesMode: c.flagDiskVolumes,
	}

	// Restore the snapshot
	op, err := d.UpdateInstance(name, req, "")
	if err != nil {
		return err
	}

	return op.Wait()
}
