package main

import (
	"github.com/lxc/lxd"
)

type pingCmd struct {
	httpAddr string
}

const pingUsage = `
Pings the lxd instance to check if it is up and working.

lxc ping <remote>
`

func (c *pingCmd) usage() string {
	return pingUsage
}

func (c *pingCmd) flags() {}

func (c *pingCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		remote = args[0]
	} else {
		remote = config.DefaultRemote
	}

	// NewClient will ping the server to test the connection before returning.
	_, _, err := lxd.NewClient(config, remote)
	return err
}
