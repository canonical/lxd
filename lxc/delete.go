package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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
		`Usage: lxc delete [<remote>:]<container>[/<snapshot>] [[<remote>:]<container>[/<snapshot>]...]

Delete containers and snapshots.`)
}

func (c *deleteCmd) flags() {
	gnuflag.BoolVar(&c.force, "f", false, i18n.G("Force the removal of running containers"))
	gnuflag.BoolVar(&c.force, "force", false, i18n.G("Force the removal of running containers"))
	gnuflag.BoolVar(&c.interactive, "i", false, i18n.G("Require user confirmation"))
	gnuflag.BoolVar(&c.interactive, "interactive", false, i18n.G("Require user confirmation"))
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

func (c *deleteCmd) doDelete(d lxd.ContainerServer, name string) error {
	var op *lxd.Operation
	var err error

	if shared.IsSnapshot(name) {
		// Snapshot delete
		fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		op, err = d.DeleteContainerSnapshot(fields[0], fields[1])
	} else {
		// Container delete
		op, err = d.DeleteContainer(name)
	}
	if err != nil {
		return err
	}

	return op.Wait()
}

func (c *deleteCmd) run(conf *config.Config, args []string) error {
	if len(args) == 0 {
		return errArgs
	}

	for _, nameArg := range args {
		remote, name, err := conf.ParseRemote(nameArg)
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
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

		ct, _, err := d.GetContainer(name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
			if !c.force {
				return fmt.Errorf(i18n.G("The container is currently running, stop it first or pass --force."))
			}

			req := api.ContainerStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			}

			op, err := d.UpdateContainerState(name, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf(i18n.G("Stopping the container failed: %s"), err)
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
