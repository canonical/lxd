package main

import (
	"os"

	"github.com/lxc/lxd"
)

type execCmd struct{}

const execUsage = `
exec specified command in a container.

lxc exec container [command]
`

func (c *execCmd) usage() string {
	return execUsage
}

func (c *execCmd) flags() {}

func (c *execCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	d, name, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	return d.Exec(name, args[1:], os.Stdin, os.Stdout, os.Stderr)
}
