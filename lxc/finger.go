package main

import (
	"github.com/gosexy/gettext"

	"github.com/lxc/lxd"
)

type fingerCmd struct {
	httpAddr string
}

func (c *fingerCmd) showByDefault() bool {
	return false
}

func (c *fingerCmd) usage() string {
	return gettext.Gettext(
		"Fingers the LXD instance to check if it is up and working.\n" +
			"\n" +
			"lxc finger <remote>\n")
}

func (c *fingerCmd) flags() {}

func (c *fingerCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	if len(args) == 1 {
		remote = config.ParseRemote(args[0])
	} else {
		remote = config.DefaultRemote
	}

	// NewClient will finger the server to test the connection before returning.
	_, err := lxd.NewClient(config, remote)
	return err
}
