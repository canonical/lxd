package main

import (
	"fmt"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

type manpageCmd struct{}

func (c *manpageCmd) showByDefault() bool {
	return false
}

func (c *manpageCmd) usage() string {
	return i18n.G(
		`Prints all subcommands help to create a lxd manpage`)
}

func (c *manpageCmd) flags() {
}

func (c *manpageCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		return errArgs
	}
	for k, _ := range commands {
		commands["help"].run(nil, []string{k})
	}
	fmt.Println(shared.Version)
	return nil
}
