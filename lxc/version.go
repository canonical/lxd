package main

import (
	"fmt"

	"github.com/lxc/lxd"
)

type versionCmd struct{}

const versionUsage = `
Prints the version number of lxd.

lxd version
`

func (c *versionCmd) usage() string {
	return versionUsage
}

func (c *versionCmd) flags() {
}

func (c *versionCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		return errArgs
	}
	fmt.Println(lxd.Version)
	return nil
}
