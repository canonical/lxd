package main

import (
	"os"
	"syscall"

	"github.com/lxc/lxd"
	"golang.org/x/crypto/ssh/terminal"
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

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	cfd := syscall.Stdout
	if terminal.IsTerminal(cfd) {
		oldttystate, err := terminal.MakeRaw(cfd)
		if err != nil {
			return err
		}
		defer terminal.Restore(cfd, oldttystate)
	}

	return d.Exec(name, args[1:], os.Stdin, os.Stdout, os.Stderr)
}
