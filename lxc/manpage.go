package main

import (
	"fmt"
	"sort"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

type manpageCmd struct{}

func (c *manpageCmd) showByDefault() bool {
	return false
}

func (c *manpageCmd) usage() string {
	return i18n.G(
		`Prints all the subcommands help.`)
}

func (c *manpageCmd) flags() {
}

func (c *manpageCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		return errArgs
	}

	keys := []string{}
	for k, _ := range commands {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	header := false
	for _, k := range keys {
		if header {
			fmt.Printf("\n\n")
		}

		fmt.Printf("### lxc %s\n", k)
		commands["help"].run(nil, []string{k})
		header = true
	}

	return nil
}
