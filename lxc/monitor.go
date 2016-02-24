package main

import (
	"fmt"

	"gopkg.in/yaml.v2"

	"github.com/codegangsta/cli"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

var commandMonitor = cli.Command{
	Name:      "monitor",
	Usage:     i18n.G("Monitor activity on the LXD server."),
	ArgsUsage: i18n.G("[remote:] [--type=TYPE...]"),

	Flags: append(commandGlobalFlags,
		cli.StringSliceFlag{
			Name:  "type",
			Usage: i18n.G("Event type to listen for"),
		},
	),
	Action: commandWrapper(commmandActionMonitor),
}

func commmandActionMonitor(config *lxd.Config, context *cli.Context) error {
	var cmd = &monitorCmd{
		typeArgs: context.StringSlice("type"),
	}
	return cmd.run(config, context.Args())
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

type monitorCmd struct {
	typeArgs typeList
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

	return d.Monitor(c.typeArgs, handler)
}
