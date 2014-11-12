package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type byNameCmd struct {
	function string
	do       func(*lxd.Client, string) (string, error)
}

func (c *byNameCmd) usage() string {
	return fmt.Sprintf(`
Creates a container using the specified release and arch

lxd %s <name>
`, c.function)
}

func (c *byNameCmd) flags() {}

func (c *byNameCmd) run(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	d, name, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	data, err := c.do(d, name)
	fmt.Println(data)
	return err
}
