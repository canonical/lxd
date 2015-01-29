package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type versionCmd struct{}

func (c *versionCmd) usage() string {
	return gettext.Gettext(
		"Prints the version number of lxd.\n" +
			"\n" +
			"lxd version\n")
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
