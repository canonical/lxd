package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdDelete struct {
	global *cmdGlobal

	flagForce          bool
	flagForceProtected bool
	flagInteractive    bool
	flagDiskVolumes    string
}

func (c *cmdDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<instance>[/<snapshot>] [[<remote>:]<instance>[/<snapshot>]...]")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete instances and snapshots"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Force the removal of running instances")
	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, "Require user confirmation")
	cmd.Flags().StringVar(&c.flagDiskVolumes, "disk-volumes", "", cli.FormatStringFlagLabel("Disk volumes mode for snapshot deletion. Possible values are \"root\" (default) and \"all-exclusive\". \"root\" only deletes the instance's root disk volume snapshot. \"all-exclusive\" deletes the instance's root disk volume snapshot and any exclusively attached volumes (non-shared) snapshots."))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstancesAction(toComplete, "delete", c.flagForce)
	}

	return cmd
}

func (c *cmdDelete) promptDelete(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Remove %s (yes/no): ", name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{"yes"}, strings.ToLower(input)) {
		return errors.New("User aborted delete operation")
	}

	return nil
}

func (c *cmdDelete) doDelete(d lxd.InstanceServer, name string) error {
	var op lxd.Operation
	var err error

	if shared.IsSnapshot(name) {
		// Snapshot delete
		fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		op, err = d.DeleteInstanceSnapshot(fields[0], fields[1], c.flagDiskVolumes)
	} else {
		// Instance delete
		op, err = d.DeleteInstance(name)
	}

	if err != nil {
		return err
	}

	return op.Wait()
}

func (c *cmdDelete) run(cmd *cobra.Command, args []string) error {
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
		connInfo, err := resource.server.GetConnectionInfo()
		if err != nil {
			return err
		}

		if c.flagInteractive {
			err := c.promptDelete(resource.name)
			if err != nil {
				return err
			}
		}

		if shared.IsSnapshot(resource.name) {
			err := c.doDelete(resource.server, resource.name)
			if err != nil {
				return fmt.Errorf("Failed deleting instance snapshot %q in project %q: %w", resource.name, connInfo.Project, err)
			}

			continue
		}

		ct, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
			if !c.flagForce {
				return errors.New("The instance is currently running, stop it first or pass --force")
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
				return fmt.Errorf("Stopping the instance failed: %s", err)
			}

			if ct.Ephemeral {
				continue
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
			return fmt.Errorf("Failed deleting instance %q in project %q: %w", resource.name, connInfo.Project, err)
		}
	}
	return nil
}
