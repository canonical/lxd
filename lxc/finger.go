package main

import (
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

type fingerCmd struct {
	httpAddr string
}

func (c *fingerCmd) showByDefault() bool {
	return false
}

func (c *fingerCmd) usage() string {
	return i18n.G(
		`Fingers the LXD instance to check if it is up and working.

lxc finger <remote>`)
}

func (c *fingerCmd) flags() {}

func (c *fingerCmd) run(config *lxd.Config, args []string) error {
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
