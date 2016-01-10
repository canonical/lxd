package main

import (
	"fmt"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared/gnuflag"
)

type monitorCmd struct{}

func (c *monitorCmd) showByDefault() bool {
	return false
}

func (c *monitorCmd) usage() string {
	return i18n.G(
		`Monitor activity on the LXD server.

lxc monitor [remote:] [--type=TYPE...]

Connects to the monitoring interface of the specified LXD server.

By default will listen to all message types.
Specific types to listen to can be specified with --type.

Example:
lxc monitor --type=logging`)
}

type typeList []string

func (f *typeList) String() string {
	return fmt.Sprint(*f)
}

func (f *typeList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("Invalid type: %s", value)
	}

	if f == nil {
		*f = make(typeList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

var typeArgs typeList

func (c *monitorCmd) flags() {
	gnuflag.Var(&typeArgs, "type", i18n.G("Event type to listen for"))
}

func (c *monitorCmd) run(config *lxd.Config, args []string) error {
	var remote string

	if len(args) > 1 {
		return errArgs
	}

	if len(args) == 0 {
		remote, _ = config.ParseRemoteAndContainer("")
	} else {
		remote, _ = config.ParseRemoteAndContainer(args[0])
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	handler := func(message interface{}) {
		render, err := yaml.Marshal(&message)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			return
		}

		fmt.Printf("%s\n\n", render)
	}

	return d.Monitor(typeArgs, handler)
}
