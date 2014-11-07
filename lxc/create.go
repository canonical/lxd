package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type createCmd struct{}

const createUsage = `
lxc create images:ubuntu <name>

Creates a container using the specified image and name
`

func (c *createCmd) usage() string {
	return createUsage
}

func (c *createCmd) flags() {}

func (c *createCmd) run(args []string) error {
	if len(args) > 2 {
		return errArgs
	}

	if len(args) < 1 {
		return errArgs
	}

	if args[0] != "images:ubuntu" {
		return fmt.Errorf("Only the default ubuntu image is supported. Try `lxc create images:ubuntu foo`.")
	}

	var containerRef string
	if len(args) == 2 {
		containerRef = args[1]
	} else {
		// TODO: come up with a random name a. la. juju/maas
		containerRef = "foo"
	}

	config, err := lxd.LoadConfig()
	if err != nil {
		return err
	}

	d, name, err := lxd.NewClient(config, containerRef)
	if err != nil {
		return err
	}

	// TODO: implement the syntax for supporting other image types/remotes
	l, err := d.Create(name, "ubuntu", "trusty", "amd64")
	if err == nil {
		fmt.Println(l)
	}
	return err
}
