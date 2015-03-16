package main

import (
	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type moveCmd struct {
	httpAddr string
}

func (c *moveCmd) showByDefault() bool {
	return true
}

func (c *moveCmd) usage() string {
	return gettext.Gettext(
		"Move containers within or in between lxd instances.\n" +
			"\n" +
			"lxc move <source container> <destination container>\n")
}

func (c *moveCmd) flags() {}

func (c *moveCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	// A move is just a copy followed by a delete.
	if err := copyContainer(config, args[0], args[1]); err != nil {
		return err
	}

	return commands["delete"].run(config, args[:1])
}
