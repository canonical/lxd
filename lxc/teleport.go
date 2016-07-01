package main

import (
	"fmt"

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

lxd teleport [remote:]container [there=:<port> here=<host>:<port>]
`)
}

func (c *teleportCmd) flags() {
}

func (c *teleportCmd) run(config *lxd.Config, args []string) error {
	// [ ] param parsing
	if len(args) < 1 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	fmt.Println(`New client: ` + d.Name)
	fmt.Println("Teleporting: " + name)

	return nil
}
