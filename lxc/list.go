package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type listCmd struct{}

func (c *listCmd) usage() string {
	return gettext.Gettext(
		"Lists the available resources.\n" +
			"\n" +
			"lxc list [resource]\n" +
			"\n" +
			"Currently resource must be a defined remote, and list only lists\n" +
			"the defined containers.\n")

}

func (c *listCmd) flags() {}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		remote = config.ParseRemote(args[0])
	} else {
		remote = config.DefaultRemote
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	cts, err := d.ListContainers()
	if err != nil {
		return err
	}

	for _, ct := range cts {
		fmt.Println(ct)
	}
	return nil
}
