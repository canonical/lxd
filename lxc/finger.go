package main

import (
	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

// TODO: Make this a "hidden" command.
var commandFinger = cli.Command{
	Name:      "finger",
	Usage:     i18n.G("Fingers the LXD instance to check if it is up and working."),
	ArgsUsage: i18n.G("<remote>"),

	Flags:  commandGlobalFlags,
	Action: commandWrapper(commandActionFinger),
}

func commandActionFinger(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		remote = config.ParseRemote(args[0])
	} else {
		remote = config.DefaultRemote
	}

	// New client may or may not need to connect to the remote host, but
	// client.ServerStatus will at least request the basic information from
	// the server.
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	_, err = client.ServerStatus()
	return err
}
