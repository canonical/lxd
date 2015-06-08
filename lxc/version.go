package main

import (
	"fmt"

	"github.com/gosexy/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type versionCmd struct{}

func (c *versionCmd) showByDefault() bool {
	return true
}

func (c *versionCmd) usage() string {
	return gettext.Gettext(
		"Prints the version number of LXD.\n" +
			"\n" +
			"lxc version\n")
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
