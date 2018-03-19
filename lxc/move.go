package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/pkg/errors"
)

type moveCmd struct {
	containerOnly bool
	mode          string
	stateless     bool
	target        string
}

func (c *moveCmd) showByDefault() bool {
	return true
}

func (c *moveCmd) usage() string {
	return i18n.G(
		`Usage: lxc move [<remote>:]<container>[/<snapshot>] [<remote>:][<container>[/<snapshot>]] [--container-only] [--target <node>]

Move containers within or in between LXD instances.

lxc move [<remote>:]<source container> [<remote>:][<destination container>] [--container-only]
    Move a container between two hosts, renaming it if destination name differs.

lxc move <old name> <new name> [--container-only]
    Rename a local container.

lxc move <container>/<old snapshot name> <container>/<new snapshot name>
    Rename a snapshot.`)
}

func (c *moveCmd) flags() {
	gnuflag.BoolVar(&c.containerOnly, "container-only", false, i18n.G("Move the container without its snapshots"))
	gnuflag.StringVar(&c.mode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay."))
	gnuflag.BoolVar(&c.stateless, "stateless", false, i18n.G("Copy a stateful container stateless"))
	gnuflag.StringVar(&c.target, "target", "", i18n.G("Node name"))
}

func (c *moveCmd) run(conf *config.Config, args []string) error {
	if len(args) != 2 && c.target == "" {
		return errArgs
	}

	// Parse the mode
	mode := "pull"
	if c.mode != "" {
		mode = c.mode
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

	// Target node and destination remote can't be used together.
	if c.target != "" && sourceRemote != destRemote {
		return fmt.Errorf(i18n.G("You must use the same source and destination remote when using --target"))
	}

	// As an optimization, if the source an destination are the same, do
	// this via a simple rename. This only works for containers that aren't
	// running, containers that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if sourceRemote == destRemote && c.target == "" {
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
	// cluster node to another. In case the rootfs of the container is
	// backed by ceph, we want to re-use the same ceph volume. This assumes
	// that the container is not running.
	if c.target != "" {
		moved, err := maybeMoveCephContainer(conf, sourceResource, destResource, c.target)
		if err != nil {
			return err
		}
		if moved {
			return nil
		}
	}

	cpy := copyCmd{}
	cpy.target = c.target

	stateful := !c.stateless

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the container.
	err = cpy.copyContainer(conf, sourceResource, destResource, true, -1, stateful, c.containerOnly, mode)
	if err != nil {
		return err
	}

	del := deleteCmd{}
	del.force = true
	return del.run(conf, args[:1])
}

// Helper to check if the container to be moved is backed by a ceph storage
// pool, and use the special POST /containers/<name>?target=<node> API if so.
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

	if shared.IsSnapshot(sourceName) {
		// TODO: implement moving snapshots.
		return false, fmt.Errorf("Moving ceph snapshots is not supported")
	}

	// Check if the container to be moved is backed by ceph.
	container, _, err := source.GetContainer(sourceName)
	if err != nil {
		// If we are unable to connect, we assume that the source node
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
