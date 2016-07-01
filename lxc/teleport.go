package main

import (
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

type teleportCmd struct {
}

func (c *teleportCmd) showByDefault() bool {
	return true
}

func (c *teleportCmd) usage() string {
	return i18n.G(
		`Make port from inside container available on local interface.

lxd teleport [remote:]container there=:<port> here=<host>:<port>
`)
}

func (c *teleportCmd) flags() {
}

func (c *teleportCmd) run(config *lxd.Config, args []string) error {
	if len(args) == 0 {
		return errArgs
	}
	return nil
}
