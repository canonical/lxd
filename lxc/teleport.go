package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

type teleportCmd struct {
}

func (c *teleportCmd) showByDefault() bool {
	return true
}

func (c *teleportCmd) usage() string {
	return i18n.G(
		`Makes port from inside container available on local interface.

lxd teleport [remote:]container there=:<port> here=<host>:<port>
`)

}
