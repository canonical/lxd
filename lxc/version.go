package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

type versionCmd struct{}

func (c *versionCmd) showByDefault() bool {
	return false
}

func (c *versionCmd) usage() string {
	return i18n.G(
		`Prints the version number of this client tool.

lxc version`)
}

func (c *versionCmd) flags() {
}

func (c *versionCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		return errArgs
	}
	fmt.Println(shared.Version)
	return nil
}
