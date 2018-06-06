package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/pkg/errors"
)

type cmdMove struct {
	global *cmdGlobal

	flagContainerOnly bool
	flagMode          string
	flagStateless     bool
	flagStorage       string
	flagTarget        string
}

func (c *cmdMove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("move [<remote>:]<container>[/<snapshot>] [<remote>:][<container>[/<snapshot>]]")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move containers within or in between LXD instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Move containers within or in between LXD instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc move [<remote>:]<source container> [<remote>:][<destination container>] [--container-only]
    Move a container between two hosts, renaming it if destination name differs.

lxc move <old name> <new name> [--container-only]
    Rename a local container.

lxc move <container>/<old snapshot name> <container>/<new snapshot name>
    Rename a snapshot.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagContainerOnly, "container-only", false, i18n.G("Move the container without its snapshots"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay.")+"``")
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful container stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdMove) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	if c.flagTarget == "" {
		exit, err := c.global.CheckArgs(cmd, args, 2, 2)
		if exit {
			return err
		}
	} else {
		exit, err := c.global.CheckArgs(cmd, args, 1, 2)
		if exit {
			return err
		}
	}

	// Parse the mode
	mode := "pull"
	if c.flagMode != "" {
		mode = c.flagMode
	}

	sourceRemote, sourceName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	destRemote := sourceRemote
	destName := ""
	if len(args) == 2 {
		var err error
		destRemote, destName, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	}

	// Target member and destination remote can't be used together.
	if c.flagTarget != "" && sourceRemote != destRemote {
		return fmt.Errorf(i18n.G("You must use the same source and destination remote when using --target"))
	}

	// As an optimization, if the source an destination are the same, do
	// this via a simple rename. This only works for containers that aren't
	// running, containers that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if sourceRemote == destRemote && c.flagTarget == "" && c.flagStorage == "" {
		source, err := conf.GetContainerServer(sourceRemote)
		if err != nil {
			return err
		}

		if shared.IsSnapshot(sourceName) {
			// Snapshot rename
			srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
			dstFields := strings.SplitN(destName, shared.SnapshotDelimiter, 2)

			op, err := source.RenameContainerSnapshot(srcFields[0], srcFields[1], api.ContainerSnapshotPost{Name: dstFields[1]})
			if err != nil {
				return err
			}

			return op.Wait()
		}

		// Container rename
		op, err := source.RenameContainer(sourceName, api.ContainerPost{Name: destName})
		if err != nil {
			return err
		}

		return op.Wait()
	}

	sourceResource := args[0]
	destResource := sourceResource
	if len(args) == 2 {
		destResource = args[1]
	}

	// If the target option was specified, we're moving a container from a
	// cluster member to another. In case the rootfs of the container is
	// backed by ceph, we want to re-use the same ceph volume. This assumes
	// that the container is not running.
	if c.flagTarget != "" {
		moved, err := maybeMoveCephContainer(conf, sourceResource, destResource, c.flagTarget)
		if err != nil {
			return err
		}
		if moved {
			return nil
		}
	}

	cpy := cmdCopy{}
	cpy.flagTarget = c.flagTarget

	stateful := !c.flagStateless

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the container.
	err = cpy.copyContainer(conf, sourceResource, destResource, true, -1, stateful, c.flagContainerOnly, mode, c.flagStorage)
	if err != nil {
		return err
	}

	del := cmdDelete{global: c.global}
	del.flagForce = true
	return del.Run(cmd, args[:1])
}

// Helper to check if the container to be moved is backed by a ceph storage
// pool, and use the special POST /containers/<name>?target=<member> API if so.
//
// It returns false if the container is not backed by ceph, true otherwise.
func maybeMoveCephContainer(conf *config.Config, sourceResource, destResource, target string) (bool, error) {
	// Parse the source.
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return false, err
	}

	// Parse the destination.
	destRemote, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return false, err
	}

	if sourceRemote != destRemote {
		return false, fmt.Errorf(
			i18n.G("You must use the same source and destination remote when using --target"))
	}

	// Make sure we have a container or snapshot name.
	if sourceName == "" {
		return false, fmt.Errorf(i18n.G("You must specify a source container name"))
	}

	// The destination name is optional.
	if destName == "" {
		destName = sourceName
	}

	// Connect to the source host
	source, err := conf.GetContainerServer(sourceRemote)
	if err != nil {
		return false, err
	}

	// Check if the container to be moved is backed by ceph.
	container, _, err := source.GetContainer(sourceName)
	if err != nil {
		// If we are unable to connect, we assume that the source member
		// is offline, and we'll try to perform the migration. If the
		// container turns out to not be backed by ceph, the migrate
		// API will still return an error.
		if !strings.Contains(err.Error(), "Unable to connect") {
			return false, errors.Wrapf(err, "Failed to get container %s", sourceName)
		}
	}
	if container != nil {
		devices := container.Devices
		for key, value := range container.ExpandedDevices {
			devices[key] = value
		}
		_, device, err := shared.GetRootDiskDevice(devices)
		if err != nil {
			return false, errors.Wrapf(err, "Failed parse root disk device")
		}

		poolName, ok := device["pool"]
		if !ok {
			return false, nil
		}

		pool, _, err := source.GetStoragePool(poolName)
		if err != nil {
			return false, errors.Wrapf(err, "Failed get root disk device pool %s", poolName)
		}
		if pool.Driver != "ceph" {
			return false, nil
		}
	}

	// The migrate API will do the right thing when passed a target.
	source = source.UseTarget(target)
	req := api.ContainerPost{Name: destName, Migration: true}
	op, err := source.MigrateContainer(sourceName, req)
	if err != nil {
		return false, err
	}
	err = op.Wait()
	if err != nil {
		return false, err
	}
	return true, nil
}
