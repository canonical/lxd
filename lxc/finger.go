package main

import (
	"github.com/lxc/lxd"
)

type fingerCmd struct {
	httpAddr string
}

const fingerUsage = `
Fingers the lxd instance to check if it is up and working.

lxc finger <remote>
`

func (c *fingerCmd) usage() string {
	return fingerUsage
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

	// NewClient will finger the server to test the connection before returning.
	_, err := lxd.NewClient(config, remote)
	return err
}
