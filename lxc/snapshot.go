package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdSnapshot struct {
	global *cmdGlobal

	flagStateful bool
	flagNoExpiry bool
	flagReuse    bool
	flagVolumes  string
}

func (c *cmdSnapshot) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("snapshot", i18n.G("[<remote>:]<instance> [<snapshot name>]"))
	cmd.Short = i18n.G("Create instance snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create instance snapshots

When --stateful is used, LXD attempts to checkpoint the instance's
running state, including process memory state, TCP connections, ...`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc snapshot u1 snap0
	Create a snapshot of "u1" called "snap0".

	lxc snapshot u1 snap0 < config.yaml
		Create a snapshot of "u1" called "snap0" with the configuration from "config.yaml".`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to snapshot the instance's running state"))
	cmd.Flags().BoolVar(&c.flagNoExpiry, "no-expiry", false, i18n.G("Ignore any configured auto-expiry for the instance"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the snapshot name already exists, delete and create a new one"))
	cmd.Flags().StringVar(&c.flagVolumes, "volumes", "", i18n.G(`Which volumes should be included in the snapshot; can be "root", "all" or "exclusive"`))

	return cmd
}

func (c *cmdSnapshot) run(cmd *cobra.Command, args []string) error {
	var stdinData api.InstanceSnapshotPut
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
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

	if shared.IsSnapshot(name) {
		if snapname != "" {
			return fmt.Errorf(i18n.G("Invalid instance name: %s"), name)
		}

		fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		name = fields[0]
		snapname = fields[1]
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if c.flagReuse && snapname != "" {
		snap, _, _ := d.GetInstanceSnapshot(name, snapname)
		if snap != nil {
			op, err := d.DeleteInstanceSnapshot(name, snapname)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}
	}

	req := api.InstanceSnapshotsPost{
		Name:     snapname,
		Stateful: c.flagStateful,
		Volumes:  c.flagVolumes,
	}

	if !stdinData.ExpiresAt.IsZero() {
		req.ExpiresAt = &stdinData.ExpiresAt
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
