package main

import (
	"fmt"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/i18n"
)

type renameCmd struct {
}

func (c *renameCmd) showByDefault() bool {
	return true
}

func (c *renameCmd) usage() string {
	return i18n.G(
		`Usage: lxc rename [<remote>:]<container>[/<snapshot>] [<container>[/<snapshot>]]

Rename a container or snapshot.`)
}

func (c *renameCmd) flags() {}

func (c *renameCmd) run(conf *config.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	sourceRemote, _, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	destRemote, _, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}
	if sourceRemote != destRemote {
		return fmt.Errorf(i18n.G("Can't specify a different remote for rename."))
	}
	move := moveCmd{}
	return move.run(conf, args)
}
