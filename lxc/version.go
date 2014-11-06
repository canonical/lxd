package main

import (
	"fmt"

	"github.com/lxc/lxd"
)

type versionCmd struct{}

const versionUsage = `
lxd version

Prints the version number of lxd.
`

func (c *versionCmd) usage() string {
	return versionUsage
}

func (c *versionCmd) flags() {
}

func (c *versionCmd) run(args []string) error {
	if len(args) > 0 {
		return errArgs
	}
	fmt.Println(lxd.Version)
	return nil
}
