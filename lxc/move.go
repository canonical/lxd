package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdMove struct {
	global *cmdGlobal

	flagNoProfiles        bool
	flagProfile           []string
	flagConfig            []string
	flagInstanceOnly      bool
	flagDevice            []string
	flagMode              string
	flagStateless         bool
	flagStorage           string
	flagTarget            string
	flagTargetProject     string
	flagAllowInconsistent bool
}

func (c *cmdMove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("move", i18n.G("[<remote>:]<instance>[/<snapshot>] [<remote>:][<instance>[/<snapshot>]]"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move instances within or in between LXD servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Move instances within or in between LXD servers

Transfer modes (--mode):
 - pull: Target server pulls the data from the source server (source must listen on network)
 - push: Source server pushes the data to the target server (target must listen on network)
 - relay: The CLI connects to both source and server and proxies the data (both source and target must listen on network)

The pull transfer mode is the default as it is compatible with all LXD versions.
`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc move [<remote>:]<source instance> [<remote>:][<destination instance>] [--instance-only]
    Move an instance between two hosts, renaming it if destination name differs.

lxc move <old name> <new name> [--instance-only]
    Rename a local instance.

lxc move <instance>/<old snapshot name> <instance>/<new snapshot name>
    Rename a snapshot.`))

	cmd.RunE = c.run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the target instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the target instance")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Unset all profiles on the target instance"))
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false, i18n.G("Move the instance without its snapshots"))
	cmd.Flags().StringVar(&c.flagMode, "mode", moveDefaultMode, i18n.G("Transfer mode. One of pull, push or relay.")+"``")
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful instance as stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagAllowInconsistent, "allow-inconsistent", false, i18n.G("Ignore copy errors for volatile files"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			instances, directives := c.global.cmpTopLevelResource("instance", toComplete)
			return instances, directives | cobra.ShellCompDirectiveNoSpace
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	_ = cmd.RegisterFlagCompletionFunc("mode", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"pull", "push", "relay"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func (c *cmdMove) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	if c.flagTarget == "" && c.flagTargetProject == "" && c.flagStorage == "" {
		exit, err := c.global.CheckArgs(cmd, args, 2, 2)
		if exit {
			return err
		}
	} else {
		exit, err := c.global.CheckArgs(cmd, args, 1, 2)
		if exit {
			return err
		}
	}

	// Parse the mode
	mode := moveDefaultMode
	if c.flagMode != "" {
		mode = c.flagMode
	}

	sourceRemote, sourceName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	destRemote := sourceRemote
	destName := ""
	if len(args) == 2 {
		var err error
		destRemote, destName, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	}

	// As an optimization, if the source an destination are the same, do
	// this via a simple rename. This only works for instances that aren't
	// running, instances that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if sourceRemote == destRemote && c.flagTarget == "" && c.flagStorage == "" && c.flagTargetProject == "" {
		if c.flagConfig != nil || c.flagDevice != nil || c.flagProfile != nil || c.flagNoProfiles {
			return errors.New(i18n.G("Can't override configuration or profiles in local rename"))
		}

		source, err := conf.GetInstanceServer(sourceRemote)
		if err != nil {
			return err
		}

		if shared.IsSnapshot(sourceName) {
			// Snapshot rename
			srcParent, srcSnap, _ := api.GetParentAndSnapshotName(sourceName)
			dstParent, dstSnap, dstIsSnap := api.GetParentAndSnapshotName(destName)

			if srcParent != dstParent {
				return errors.New(i18n.G("Invalid new snapshot name, parent must be the same as source"))
			}

			if !dstIsSnap {
				return errors.New(i18n.G("Invalid new snapshot name"))
			}

			op, err := source.RenameInstanceSnapshot(srcParent, srcSnap, api.InstanceSnapshotPost{Name: dstSnap})
			if err != nil {
				return err
			}

			return op.Wait()
		}

		// Instance rename
		op, err := source.RenameInstance(sourceName, api.InstancePost{Name: destName})
		if err != nil {
			return err
		}

		return op.Wait()
	}

	sourceResource := args[0]
	destResource := sourceResource
	if len(args) == 2 {
		destResource = args[1]
	}

	stateful := !c.flagStateless

	if c.flagTarget != "" {
		// If the target option was specified, we're moving an instance from a
		// cluster member to another, let's use the dedicated API.
		if sourceRemote == destRemote {
			if c.flagInstanceOnly {
				return errors.New(i18n.G("The --instance-only flag can't be used with --target"))
			}

			if c.flagStorage != "" {
				return errors.New(i18n.G("The --storage flag can't be used with --target"))
			}

			if c.flagTargetProject != "" {
				return errors.New(i18n.G("The --target-project flag can't be used with --target"))
			}

			if c.flagMode != moveDefaultMode {
				return errors.New(i18n.G("The --mode flag can't be used with --target"))
			}

			return moveClusterInstance(conf, sourceResource, destResource, c.flagTarget, c.global.flagQuiet, stateful)
		}

		dest, err := conf.GetInstanceServer(destRemote)
		if err != nil {
			return err
		}

		if !dest.IsClustered() {
			return errors.New(i18n.G("The destination LXD server is not clustered"))
		}
	}

	// Support for server-side move. Currently, such migration can only move an instance to different project
	// or storage pool. If specific profile, device or config is provided, the instance should be copied (move using copy).
	if sourceRemote == destRemote && (c.flagStorage != "" || c.flagTargetProject != "") {
		source, err := conf.GetInstanceServer(sourceRemote)
		if err != nil {
			return err
		}

		if source.HasExtension("instance_move_config") {
			if c.flagMode != moveDefaultMode {
				return errors.New(i18n.G("The --mode flag can't be used with --storage or --target-project"))
			}

			// Evaluate provided profiles. If no profiles were provided and flag noProfiles is false,
			// existing instance profiles will be retained. Otherwise, flags are respected.
			var profiles *[]string
			if len(c.flagProfile) > 0 {
				profiles = &c.flagProfile
			} else if c.flagNoProfiles {
				profiles = &[]string{}
			}

			return c.moveInstance(conf, sourceResource, destResource, profiles, stateful)
		}

		if source.HasExtension("instance_pool_move") && source.HasExtension("instance_project_move") {
			if len(c.flagConfig) != 0 || len(c.flagDevice) != 0 || len(c.flagProfile) != 0 || c.flagNoProfiles {
				return fmt.Errorf("The move command does not support flags --config, --device, --profile, and --no-profiles. Please use copy instead")
			}

			if c.flagMode != moveDefaultMode {
				return errors.New(i18n.G("The --mode flag can't be used with --storage or --target-project"))
			}

			c.flagConfig = []string{}
			c.flagDevice = []string{}
			return c.moveInstance(conf, sourceResource, destResource, nil, stateful)
		}
	}

	cpy := cmdCopy{}
	cpy.global = c.global
	cpy.flagTarget = c.flagTarget
	cpy.flagTargetProject = c.flagTargetProject
	cpy.flagConfig = c.flagConfig
	cpy.flagDevice = c.flagDevice
	cpy.flagProfile = c.flagProfile
	cpy.flagNoProfiles = c.flagNoProfiles
	cpy.flagAllowInconsistent = c.flagAllowInconsistent

	instanceOnly := c.flagInstanceOnly

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the instance.
	err = cpy.copyInstance(conf, sourceResource, destResource, true, -1, stateful, instanceOnly, mode, c.flagStorage, true)
	if err != nil {
		return err
	}

	del := cmdDelete{global: c.global}
	del.flagForce = true
	del.flagForceProtected = true
	err = del.run(cmd, args[:1])
	if err != nil {
		return fmt.Errorf("Failed to delete original instance after copying it: %w", err)
	}

	return nil
}

// Move an instance using special POST /instances/<name>?target=<member> API.
func moveClusterInstance(conf *config.Config, sourceResource string, destResource string, target string, quiet bool, stateful bool) error {
	// Parse the source.
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return err
	}

	// Parse the destination.
	_, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return err
	}

	// Make sure we have an instance or snapshot name.
	if sourceName == "" {
		return errors.New(i18n.G("You must specify a source instance name"))
	}

	// The destination name is optional.
	if destName == "" {
		destName = sourceName
	}

	// Connect to the source host
	source, err := conf.GetInstanceServer(sourceRemote)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to connect to cluster member: %w"), err)
	}

	// Check that it's a cluster
	if !source.IsClustered() {
		return errors.New(i18n.G("The source LXD server is not clustered"))
	}

	// The migrate API will do the right thing when passed a target.
	source = source.UseTarget(target)
	req := api.InstancePost{
		Name:      destName,
		Migration: true,
		Live:      stateful,
	}

	op, err := source.MigrateInstance(sourceName, req)
	if err != nil {
		return fmt.Errorf(i18n.G("Migration API failure: %w"), err)
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Transferring instance: %s"),
		Quiet:  quiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}
	// Wait for the move to complete
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	err = op.Wait()
	if err != nil {
		return fmt.Errorf(i18n.G("Migration operation failure: %w"), err)
	}

	return nil
}

// Move an instance between pools and projects using special POST /instances/<name> API.
func (c *cmdMove) moveInstance(conf *config.Config, sourceResource string, destResource string, profiles *[]string, stateful bool) error {
	// Parse the source.
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return err
	}

	// Parse the destination.
	_, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return err
	}

	// Make sure we have an instance or snapshot name.
	if sourceName == "" {
		return errors.New(i18n.G("You must specify a source instance name"))
	}

	// The destination name is optional.
	if destName == "" {
		destName = sourceName
	}

	// Connect to the source host.
	source, err := conf.GetInstanceServer(sourceRemote)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to connect to cluster member: %w"), err)
	}

	// Copy of an instance into a new instance.
	inst, _, err := source.GetInstance(sourceName)
	if err != nil {
		return err
	}

	// Overwrite the config values.
	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
		}

		inst.Config[key] = value
	}

	// Parse device map and overwrite device settings.
	deviceMap, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return err
	}

	for k, m := range deviceMap {
		if inst.Devices[k] == nil {
			inst.Devices[k] = m
			continue
		}

		for key, value := range m {
			inst.Devices[k][key] = value
		}
	}

	// Pass the new pool to the migration API.
	req := api.InstancePost{
		Name:         destName,
		Migration:    true,
		InstanceOnly: c.flagInstanceOnly,
		Pool:         c.flagStorage,
		Project:      c.flagTargetProject,
		Live:         stateful,
		Config:       inst.Config,
		Devices:      inst.Devices,
	}

	// Overwrite profiles.
	if profiles != nil {
		req.Profiles = *profiles
	}

	// Traditionally, if instance with snapshots is transferred across projects,
	// the snapshots keep their own profiles.
	// This doesn't work if the snapshot profiles don't exist in the target project.
	// If different profiles are specified for the instance,
	// instruct the server to apply the profiles of the source instance to the snapshots as well.
	if c.flagNoProfiles || profiles != nil {
		req.OverrideSnapshotProfiles = true
	}

	op, err := source.MigrateInstance(sourceName, req)
	if err != nil {
		return fmt.Errorf(i18n.G("Migration API failure: %w"), err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf(i18n.G("Migration operation failure: %w"), err)
	}

	return nil
}

// Default migration mode when moving an instance.
const moveDefaultMode = "pull"
