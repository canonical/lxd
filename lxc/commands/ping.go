package commands

import (
	"errors"

	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	//"github.com/lxc/lxd/lxc/modules/settings"

)

var (
	ErrPingArgs = errors.New("ping: too many subcommand arguments")
)

var Ping = cli.Command{
	Name:  "ping",
	Usage: "Pings the lxd instance",
	Description: "Check if the lxd instance is up and working.",
	Before: runPing,
	Flags:  []cli.Flag{},
}

func runPing(ctx *cli.Context) error {
	args := ctx.Args()

	if len(args) > 1 {
		return ErrPingArgs
	}

	config, err := lxd.LoadConfig()
	if err != nil {
		return err
	}

	var remote string
	if len(args) == 1 {
		remote = args[0]
	} else {
		remote = config.DefaultRemote
	}

	// NewClient will ping the server to test the connection before returning.
	_, _, err = lxd.NewClient(config, remote)
	return err
}

/* vim: set noet ts=4 sw=4 sts=4: */
