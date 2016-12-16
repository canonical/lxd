package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/version"
)

type versionCmd struct{}

func (c *versionCmd) showByDefault() bool {
	return true
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
	fmt.Println(version.Version)
	return nil
}
