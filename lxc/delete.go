package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type deleteCmd struct {
	force       bool
	interactive bool
}

func (c *deleteCmd) showByDefault() bool {
	return true
}

func (c *deleteCmd) usage() string {
	return i18n.G(
		`Delete containers or container snapshots.

lxc delete [remote:]<container>[/<snapshot>] [remote:][<container>[/<snapshot>]...]

Destroy containers or snapshots with any attached data (configuration, snapshots, ...).`)
}

func (c *deleteCmd) flags() {
	gnuflag.BoolVar(&c.force, "f", false, i18n.G("Force the removal of stopped containers."))
	gnuflag.BoolVar(&c.force, "force", false, i18n.G("Force the removal of stopped containers."))
	gnuflag.BoolVar(&c.interactive, "i", false, i18n.G("Require user confirmation."))
	gnuflag.BoolVar(&c.interactive, "interactive", false, i18n.G("Require user confirmation."))
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

			resp, err := d.Action(name, shared.Stop, -1, true)
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
