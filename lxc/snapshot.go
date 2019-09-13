package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"

	"time"
)

type cmdSnapshot struct {
	global *cmdGlobal

	flagStateful bool
	flagNoExpiry bool
}

func (c *cmdSnapshot) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("snapshot [<remote>:]<container> [<snapshot name>]")
	cmd.Short = i18n.G("Create container snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create container snapshots

When --stateful is used, LXD attempts to checkpoint the container's
running state, including process memory state, TCP connections, ...`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc snapshot u1 snap0
    Create a snapshot of "u1" called "snap0".`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to snapshot the container's running state"))
	cmd.Flags().BoolVar(&c.flagNoExpiry, "no-expiry", false, i18n.G("Ignore any configured auto-expiry for the container"))

	return cmd
}

func (c *cmdSnapshot) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	var snapname string
	if len(args) < 2 {
		snapname = ""
	} else {
		snapname = args[1]
	}

	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	req := api.InstanceSnapshotsPost{
		Name:     snapname,
		Stateful: c.flagStateful,
	}

	if c.flagNoExpiry {
		req.ExpiresAt = &time.Time{}
	}

	op, err := d.CreateInstanceSnapshot(name, req)
	if err != nil {
		return err
	}

	return op.Wait()
}
