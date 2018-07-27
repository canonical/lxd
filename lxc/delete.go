package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdDelete struct {
	global *cmdGlobal

	flagForce       bool
	flagInteractive bool
}

func (c *cmdDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<container>[/<snapshot>] [[<remote>:]<container>[/<snapshot>]...]")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete containers and snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete containers and snapshots`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the removal of running containers"))
	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))

	return cmd
}

func (c *cmdDelete) promptDelete(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove %s (yes/no): "), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !shared.StringInSlice(strings.ToLower(input), []string{i18n.G("yes")}) {
		return fmt.Errorf(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdDelete) doDelete(d lxd.ContainerServer, name string) error {
	var op lxd.Operation
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

func (c *cmdDelete) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	for _, resource := range resources {
		if c.flagInteractive {
			err := c.promptDelete(resource.name)
			if err != nil {
				return err
			}
		}

		if shared.IsSnapshot(resource.name) {
			return c.doDelete(resource.server, resource.name)
		}

		ct, _, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
			if !c.flagForce {
				return fmt.Errorf(i18n.G("The container is currently running, stop it first or pass --force"))
			}

			req := api.ContainerStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			}

			op, err := resource.server.UpdateContainerState(resource.name, req, "")
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

		if err := c.doDelete(resource.server, resource.name); err != nil {
			return err
		}
	}
	return nil
}
