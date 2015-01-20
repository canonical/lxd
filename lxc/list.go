package main

import (
	"fmt"
	"github.com/lxc/lxd"
	"strings"
)

type listCmd struct{}

const listUsage = `
Lists the available resources.

lxc list [resource]

Currently resource must be a defined remote, and list only lists
the defined containers.
`

func (c *listCmd) usage() string {
	return listUsage
}

func (c *listCmd) flags() {}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var cts []string
	var remote string

	if len(args) == 1 {
		remote = args[0]
	} else {
		remote = config.DefaultRemote
	}

	if remote == "" {
		d, _, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		cts, err = d.ListContainers()
		if err != nil {
			return err
		}
	} else {
		var rm lxd.RegistryManager
		rm.InitRegistryManager()
		err := rm.FetchImageServerData()
		if err != nil {
			return err
		}

		image_server := strings.SplitN(remote, ":", 2)
		if image_server[1] == "" {
			cts, err = rm.GetImageServers()
		} else {
			cts, err = rm.GetImageList(image_server[1])

		}
		if err != nil {
			return err
		}
	}

	for _, ct := range cts {
		fmt.Println(ct)
	}
	return nil
}
