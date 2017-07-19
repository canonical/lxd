package main

import (
	"fmt"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
)

type launchCmd struct {
	init initCmd
}

func (c *launchCmd) showByDefault() bool {
	return true
}

func (c *launchCmd) usage() string {
	return i18n.G(
		`Usage: lxc launch [<remote>:]<image> [<remote>:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...] [--network|-n <network>] [--storage|-s <pool>] [--type|-t <instance type>]

Create and start containers from images.

Not specifying -p will result in the default profile.
Specifying "-p" with no argument will result in no profile.

Examples:
    lxc launch ubuntu:16.04 u1`)
}

func (c *launchCmd) flags() {
	c.init = initCmd{}
	c.init.flags()
}

func (c *launchCmd) run(conf *config.Config, args []string) error {
	// Call the matching code from init
	d, name, err := c.init.create(conf, args)
	if err != nil {
		return err
	}

	// Get the remote
	var remote string
	if len(args) == 2 {
		remote, _, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	} else {
		remote, _, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	// Start the container
	fmt.Printf(i18n.G("Starting %s")+"\n", name)
	req := api.ContainerStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err := d.UpdateContainerState(name, req, "")
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		prettyName := name
		if remote != "" {
			prettyName = fmt.Sprintf("%s:%s", remote, name)
		}

		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, prettyName)
	}

	return nil
}
