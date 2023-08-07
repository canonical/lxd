package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdDelete struct {
	global *cmdGlobal

	flagForce          bool
	flagForceProtected bool
	flagInteractive    bool
}

// Constructs the 'delete' command with its usage, descriptions, flags, and bind its execution function for deleting instances and snapshots.
func (c *cmdDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance>[/<snapshot>] [[<remote>:]<instance>[/<snapshot>]...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete instances and snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete instances and snapshots`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the removal of running instances"))
	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))

	return cmd
}

// Prompts user for confirmation to delete a specified entity and aborts operation if the input isn't affirmative.
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

// Executes the deletion of an instance or snapshot, returning an error if the operation fails.
func (c *cmdDelete) doDelete(d lxd.InstanceServer, name string) error {
	var op lxd.Operation
	var err error

	if shared.IsSnapshot(name) {
		// Snapshot delete
		fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		op, err = d.DeleteInstanceSnapshot(fields[0], fields[1])
	} else {
		// Instance delete
		op, err = d.DeleteInstance(name)
	}

	if err != nil {
		return err
	}

	return op.Wait()
}

// Coordinates deletion of provided instances or snapshots, prompting user confirmation if necessary and handling force deletions.
func (c *cmdDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	// Check that everything exists.
	err = instancesExist(resources)
	if err != nil {
		return err
	}

	// Process with deletion.
	for _, resource := range resources {
		if c.flagInteractive {
			err := c.promptDelete(resource.name)
			if err != nil {
				return err
			}
		}

		if shared.IsSnapshot(resource.name) {
			err := c.doDelete(resource.server, resource.name)
			if err != nil {
				return err
			}

			continue
		}

		ct, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
			if !c.flagForce {
				return fmt.Errorf(i18n.G("The instance is currently running, stop it first or pass --force"))
			}

			req := api.InstanceStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			}

			op, err := resource.server.UpdateInstanceState(resource.name, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf(i18n.G("Stopping the instance failed: %s"), err)
			}

			if ct.Ephemeral {
				return nil
			}
		}

		if c.flagForceProtected && shared.IsTrue(ct.ExpandedConfig["security.protection.delete"]) {
			// Refresh in case we had to stop it above.
			ct, etag, err := resource.server.GetInstance(resource.name)
			if err != nil {
				return err
			}

			ct.Config["security.protection.delete"] = "false"
			op, err := resource.server.UpdateInstance(resource.name, ct.Writable(), etag)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}

		err = c.doDelete(resource.server, resource.name)
		if err != nil {
			return err
		}
	}
	return nil
}
