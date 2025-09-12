package main

import (
	"bufio"
	"encoding/json"
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
	"github.com/canonical/lxd/shared/i18n"
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
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance>[/<snapshot>] [[<remote>:]<instance>[/<snapshot>]...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete instances and snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete instances and snapshots`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the removal of running instances"))
	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))
	cmd.Flags().StringVar(&c.flagDiskVolumes, "disk-volumes", "", i18n.G(`Disk volumes mode. Possible values are "root" (default) and "all-exclusive". "root" only deletes the snapshot for an instance's root disk. "all-exclusive" deletes snapshots for the instance's root disk and any exclusively attached volumes (non-shared).`))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstancesAction(toComplete, "delete", c.flagForce)
	}

	return cmd
}

func (c *cmdDelete) promptDelete(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove %s (yes/no): "), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return errors.New(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdDelete) doDelete(d lxd.InstanceServer, name string) error {
	var op lxd.Operation
	var err error

	if shared.IsSnapshot(name) {
		// Snapshot delete
		fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)

		// Resolve attached volume snapshots to delete if requested.
		var attachedSnapshotUUIDs map[string]string
		if c.flagDiskVolumes == api.DiskVolumesModeAllExclusive {
			snap, _, err := d.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			raw := snap.Config["volatile.attached_volumes"]
			if raw != "" {
				// Parse JSON map: attached volume UUID -> snapshot UUID.
				err := json.Unmarshal([]byte(raw), &attachedSnapshotUUIDs)
				if err != nil {
					return fmt.Errorf("Failed to parse volatile.attached_volumes: %w", err)
				}
			}
		}

		op, err = d.DeleteInstanceSnapshot(fields[0], fields[1])
		if err != nil {
			return err
		}

		err := op.Wait()
		if err != nil {
			return err
		}

		if c.flagDiskVolumes == api.DiskVolumesModeAllExclusive && len(attachedSnapshotUUIDs) > 0 {
			vols, err := d.GetVolumesWithFilterAllProjects([]string{"type=custom"})
			if err != nil {
				return fmt.Errorf("Failed getting volumes to delete attached snapshots: %w", err)
			}

			// Build reverse lookup from snapshot UUID to volume record(s).
			targetUUIDs := make(map[string]struct{}, len(attachedSnapshotUUIDs))
			for _, snapUUID := range attachedSnapshotUUIDs {
				targetUUIDs[snapUUID] = struct{}{}
			}

			// For each matching snapshot volume, delete it.
			for _, v := range vols {
				// Only consider snapshot volumes.
				volName, snapName, isSnap := api.GetParentAndSnapshotName(v.Name)
				if !isSnap {
					continue
				}

				if v.Config == nil {
					continue
				}

				uuid, ok := v.Config["volatile.uuid"]
				if ok {
					_, ok := targetUUIDs[uuid]
					if ok {
						op, err := d.DeleteStoragePoolVolumeSnapshot(v.Pool, v.Type, volName, snapName)
						if err != nil {
							return fmt.Errorf("Failed deleting attached volume snapshot %q: %w", v.Name, err)
						}

						err = op.Wait()
						if err != nil {
							return err
						}
					}
				}
			}
		}
	} else {
		// Instance delete.
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
				return fmt.Errorf(i18n.G("Failed deleting instance snapshot %q in project %q: %w"), resource.name, connInfo.Project, err)
			}

			continue
		}

		ct, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
			if !c.flagForce {
				return errors.New(i18n.G("The instance is currently running, stop it first or pass --force"))
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
			return fmt.Errorf(i18n.G("Failed deleting instance %q in project %q: %w"), resource.name, connInfo.Project, err)
		}
	}
	return nil
}
