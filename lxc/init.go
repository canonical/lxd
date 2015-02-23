package main

import (
	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type initCmd struct{}

func (c *initCmd) showByDefault() bool {
	return false
}

func (c *initCmd) usage() string {
	return gettext.Gettext(
		"lxc init ubuntu [<name>]\n" +
			"\n" +
			"Initializes a container using the specified image and name.\n")
}

func (c *initCmd) flags() {}

func (c *initCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 2 || len(args) < 1 {
		return errArgs
	}

	/* TODO: image could also be a resource:name */
	image := args[0]

	var name string
	var remote string
	if len(args) == 2 {
		remote, name = config.ParseRemoteAndContainer(args[1])
	} else {
		name = ""
		remote = ""
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	// TODO: implement the syntax for supporting other image types/remotes
	resp, err := d.Init(name, image)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
