package main

import (
	"fmt"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/version"
)

type versionCmd struct{}

func (c *versionCmd) showByDefault() bool {
	return false
}

func (c *versionCmd) usage() string {
	return i18n.G(
		`Usage: lxc version

Print the version number of this client tool.`)
}

func (c *versionCmd) flags() {
}

func (c *versionCmd) run(conf *config.Config, args []string) error {
	if len(args) > 0 {
		return errArgs
	}
	fmt.Println(version.Version)
	return nil
}
