package main

import (
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/i18n"
)

type fingerCmd struct{}

func (c *fingerCmd) showByDefault() bool {
	return false
}

func (c *fingerCmd) usage() string {
	return i18n.G(
		`Usage: lxc finger [<remote>:]

Check if the LXD server is alive.`)
}

func (c *fingerCmd) flags() {}

func (c *fingerCmd) run(conf *config.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		var err error
		remote, _, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	} else {
		remote = conf.DefaultRemote
	}

	// New client may or may not need to connect to the remote host, but
	// client.ServerStatus will at least request the basic information from
	// the server.
	client, err := lxd.NewClient(conf.Legacy(), remote)
	if err != nil {
		return err
	}

	_, err = client.ServerStatus()
	return err
}
