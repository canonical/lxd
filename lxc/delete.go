package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandDelete = cli.Command{
	Name:        "delete",
	Usage:       i18n.G("Delete containers or container snapshots."),
	ArgsUsage:   i18n.G("[remote:]<container>[/<snapshot>] [remote:][<container>[/<snapshot>]...] [--force|-f] [--interactive|-i]"),
	Description: i18n.G("Destroy containers or snapshots with any attached data (configuration, snapshots, ...)."),

	Flags: commandGlobalFlagsWrapper(
		cli.BoolFlag{
			Name:  "force, f",
			Usage: i18n.G("Force the removal of stopped containers."),
		},

		cli.BoolFlag{
			Name:  "interactive, i",
			Usage: i18n.G("Require user confirmation."),
		},
	),
	Action: commandWrapper(commandActionDelete),
}

func commandActionDelete(config *lxd.Config, context *cli.Context) error {
	var cmd = &deleteCmd{}
	cmd.force = context.Bool("force")
	cmd.interactive = context.Bool("interactive")
	return cmd.run(config, context.Args())
}

type deleteCmd struct {
	force       bool
	interactive bool
}

func (c *deleteCmd) promptDelete(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove %s (yes/no): "), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")
	if !shared.StringInSlice(strings.ToLower(input), []string{i18n.G("yes")}) {
		return fmt.Errorf(i18n.G("User aborted delete operation."))
	}

	return nil
}

func (c *deleteCmd) doDelete(d *lxd.Client, name string) error {
	resp, err := d.Delete(name)
	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}

func (c *deleteCmd) run(config *lxd.Config, args []string) error {
	if len(args) == 0 {
		return errArgs
	}

	for _, nameArg := range args {
		remote, name := config.ParseRemoteAndContainer(nameArg)

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		if c.interactive {
			err := c.promptDelete(name)
			if err != nil {
				return err
			}
		}

		if shared.IsSnapshot(name) {
			return c.doDelete(d, name)
		}

		ct, err := d.ContainerInfo(name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != shared.Stopped {
			if !c.force {
				return fmt.Errorf(i18n.G("The container is currently running, stop it first or pass --force."))
			}

			resp, err := d.Action(name, shared.Stop, -1, true, false)
			if err != nil {
				return err
			}

			op, err := d.WaitFor(resp.Operation)
			if err != nil {
				return err
			}

			if op.StatusCode == shared.Failure {
				return fmt.Errorf(i18n.G("Stopping container failed!"))
			}

			if ct.Ephemeral == true {
				return nil
			}
		}

		if err := c.doDelete(d, name); err != nil {
			return err
		}
	}
	return nil
}
