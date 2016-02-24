package main

import (
	"fmt"

	"github.com/codegangsta/cli"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandStart = cli.Command{
	Name:      "start",
	Usage:     i18n.G("Changes one or more containers state to start."),
	ArgsUsage: i18n.G("<name> [<name>...]"),

	Flags: append(commandGlobalFlags,
		cli.BoolFlag{
			Name:  "stateless",
			Usage: i18n.G("Ignore the container state."),
		},
	),
	Action: commandWrapper(commandActionAction),
}

var commandStop = cli.Command{
	Name:      "stop",
	Usage:     i18n.G("Changes one or more containers state to stop."),
	ArgsUsage: i18n.G("<name> [<name>...]"),

	Flags: append(commandGlobalFlags,
		cli.BoolFlag{
			Name:  "force",
			Usage: i18n.G("Force the container to shutdown."),
		},
		cli.IntFlag{
			Name:  "timeout",
			Usage: i18n.G("Time to wait for the container before killing it."),
		},
		cli.BoolFlag{
			Name:  "statefull",
			Usage: i18n.G("Store the container state."),
		},
	),
	Action: commandWrapper(commandActionAction),
}

var commandRestart = cli.Command{
	Name:      "restart",
	Usage:     i18n.G("Changes one or more containers state to restart."),
	ArgsUsage: i18n.G("<name> [<name>...]"),

	Flags: append(commandGlobalFlags,
		cli.BoolFlag{
			Name:  "force",
			Usage: i18n.G("Force the container to shutdown."),
		},
		cli.IntFlag{
			Name:  "timeout",
			Usage: i18n.G("Time to wait for the container before killing it."),
		},
	),
	Action: commandWrapper(commandActionAction),
}

func commandActionAction(config *lxd.Config, c *cli.Context) error {
	var actionName = c.Command.Name
	var timeout = c.Int("timeout")
	var force = c.Bool("force")
	var args = c.Args()

	if len(args) == 0 {
		return errArgs
	}

	for _, nameArg := range args {
		remote, name := config.ParseRemoteAndContainer(nameArg)
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		if name == "" {
			return fmt.Errorf(i18n.G("Must supply container name for: ")+"\"%s\"", nameArg)
		}

		var action shared.ContainerAction
		state := false
		switch actionName {
		case "stop":
			if c.Bool("stateful") {
				state = true
			}
			action = shared.Stop
		case "start":

			action = shared.Start
		case "restart":
			action = shared.Restart
		case "freeze":
			action = shared.Freeze
		case "unfreeze":
			action = shared.Unfreeze
		}

		if action == shared.Start {
			current, err := d.ContainerInfo(name)
			if err != nil {
				return err
			}

			// "start" for a frozen container means "unfreeze"
			if current.StatusCode == shared.Frozen {
				action = shared.Unfreeze
			}

			if current.Stateful && !c.Bool("stateless") {
				state = true
			}
		}

		resp, err := d.Action(name, action, timeout, force, state)
		if err != nil {
			return err
		}

		if resp.Type != lxd.Async {
			return fmt.Errorf(i18n.G("bad result type from action"))
		}

		if err := d.WaitForSuccess(resp.Operation); err != nil {
			return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, name)
		}
	}
	return nil
}
