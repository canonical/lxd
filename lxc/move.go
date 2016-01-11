package main

import (
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
)

type moveCmd struct {
	httpAddr string
}

func (c *moveCmd) showByDefault() bool {
	return true
}

func (c *moveCmd) usage() string {
	return i18n.G(
		`Move containers within or in between lxd instances.

lxc move [remote:]<source container> [remote:]<destination container>
    Move a container between two hosts, renaming it if destination name differs.

lxc move <old name> <new name>
    Rename a local container.
`)
}

func (c *moveCmd) flags() {}

func (c *moveCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	sourceRemote, sourceName := config.ParseRemoteAndContainer(args[0])
	destRemote, destName := config.ParseRemoteAndContainer(args[1])

	// As an optimization, if the source an destination are the same, do
	// this via a simple rename. This only works for containers that aren't
	// running, containers that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if sourceRemote == destRemote {
		source, err := lxd.NewClient(config, sourceRemote)
		if err != nil {
			return err
		}

		canRename := false
		if shared.IsSnapshot(sourceName) {
			canRename = true
		} else {
			status, err := source.ContainerStatus(sourceName)
			if err != nil {
				return err
			}
			canRename = status.Status.StatusCode != shared.Running
		}

		if canRename {
			rename, err := source.Rename(sourceName, destName)
			if err != nil {
				return err
			}
			return source.WaitForSuccess(rename.Operation)
		}
	}

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the container.
	if err := copyContainer(config, args[0], args[1], true, -1); err != nil {
		return err
	}

	return commands["delete"].run(config, args[:1])
}
