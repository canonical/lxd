package main

import (
	"strings"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type moveCmd struct {
	containerOnly bool
	mode          string
	stateless     bool
}

func (c *moveCmd) showByDefault() bool {
	return true
}

func (c *moveCmd) usage() string {
	return i18n.G(
		`Usage: lxc move [<remote>:]<container>[/<snapshot>] [<remote>:][<container>[/<snapshot>]] [--container-only]

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
}

func (c *moveCmd) run(conf *config.Config, args []string) error {
	if len(args) != 2 {
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

	destRemote, destName, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	// As an optimization, if the source an destination are the same, do
	// this via a simple rename. This only works for containers that aren't
	// running, containers that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if sourceRemote == destRemote {
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

	cpy := copyCmd{}

	stateful := !c.stateless

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the container.
	err = cpy.copyContainer(conf, args[0], args[1], true, -1, stateful, c.containerOnly, mode)
	if err != nil {
		return err
	}

	del := deleteCmd{}
	del.force = true
	return del.run(conf, args[:1])
}
