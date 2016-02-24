package main

import (
	"github.com/codegangsta/cli"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

var commandMove = cli.Command{
	Name:      "move",
	Usage:     i18n.G("Move containers within or in between lxd instances."),
	ArgsUsage: i18n.G("[remote:]<source container> [remote:]<destination container>"),

	Flags:  commandGlobalFlags,
	Action: commandWrapper(commmandActionMove),
}

func commmandActionMove(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
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

		rename, err := source.Rename(sourceName, destName)
		if err != nil {
			return err
		}

		return source.WaitForSuccess(rename.Operation)
	}

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the container.
	if err := copyContainer(config, args[0], args[1], true, -1); err != nil {
		return err
	}

	var cmd = &deleteCmd{}
	return cmd.run(config, args[:1])
}
