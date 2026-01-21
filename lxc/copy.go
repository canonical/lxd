package main

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdCopy struct {
	global *cmdGlobal

	flagNoProfiles        bool
	flagProfile           []string
	flagConfig            []string
	flagDevice            []string
	flagEphemeral         bool
	flagInstanceOnly      bool
	flagMode              string
	flagStateless         bool
	flagStorage           string
	flagTarget            string
	flagTargetProject     string
	flagRefresh           bool
	flagAllowInconsistent bool
	flagStart             bool
}

func (c *cmdCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", "[<remote>:]<source>[/<snapshot>] [[<remote>:]<destination>]")
	cmd.Aliases = []string{"cp"}
	cmd.Short = "Copy instance within or in between LXD servers"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

Transfer modes (--mode):
 - pull: Target server pulls the data from the source server (source must listen on network)
 - push: Source server pushes the data to the target server (target must listen on network)
 - relay: The CLI connects to both source and server and proxies the data (both source and target must listen on network)

The pull transfer mode is the default as it is compatible with all LXD versions.
`)

	cmd.RunE = c.run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, cli.FormatStringFlagLabel("Config key/value to apply to the new instance"))
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, cli.FormatStringFlagLabel("New key/value to apply to a specific device"))
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, cli.FormatStringFlagLabel("Profile to apply to the new instance"))
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, "Ephemeral instance")
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", cli.FormatStringFlagLabel("Transfer mode. One of pull, push or relay"))
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false, "Copy the instance without its snapshots")
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, "Copy a stateful instance stateless")
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", cli.FormatStringFlagLabel("Storage pool name"))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", cli.FormatStringFlagLabel("Copy to a project different from the source"))
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, "Create the instance with no profiles applied")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, "Perform an incremental copy")
	cmd.Flags().BoolVar(&c.flagAllowInconsistent, "allow-inconsistent", false, "Ignore copy errors for volatile files")
	cmd.Flags().BoolVar(&c.flagStart, "start", false, "Start instance after copy")
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("instance", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdCopy) copyInstance(conf *config.Config, sourceResource string, destResource string, keepVolatile bool, ephemeral int, stateful bool, instanceOnly bool, mode string, pool string, move bool) error {
	// Parse the source
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return err
	}

	// Parse the destination
	destRemote, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return err
	}

	// Make sure we have an instance or snapshot name
	if sourceName == "" {
		return errors.New("You must specify a source instance name")
	}

	// Don't allow refreshing without profiles.
	if c.flagRefresh && c.flagNoProfiles {
		return errors.New("--no-profiles cannot be used with --refresh")
	}

	// Don't allow refreshing and starting the instance afterwards as not supported by the migration API.
	if c.flagRefresh && c.flagStart {
		return errors.New("--start cannot be used with --refresh")
	}

	// If the instance is being copied to a different remote and no destination name is
	// specified, use the source name with snapshot suffix trimmed (in case a new instance
	// is being created from a snapshot).
	if destName == "" && destResource != "" && c.flagTarget == "" {
		destName = strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)[0]
	}

	// Ensure that a destination name is provided.
	if destName == "" {
		return errors.New("You must specify a destination instance name")
	}

	// Connect to the source host
	source, err := conf.GetInstanceServer(sourceRemote)
	if err != nil {
		return err
	}

	// Connect to the destination host
	var dest lxd.InstanceServer
	if sourceRemote == destRemote {
		// Source and destination are the same
		dest = source
	} else {
		// Destination is different, connect to it
		dest, err = conf.GetInstanceServer(destRemote)
		if err != nil {
			return err
		}
	}

	// Project copies
	if c.flagTargetProject != "" {
		dest = dest.UseProject(c.flagTargetProject)
	}

	// Apply target flag if specified.
	if c.flagTarget != "" {
		// Confirm that --target is only used with a cluster
		if !dest.IsClustered() {
			return errors.New("To use --target, the destination remote must be a cluster")
		}

		dest = dest.UseTarget(c.flagTarget)
	}

	// Parse the config overrides
	configOverrides := map[string]string{}
	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf("Bad key=value pair: %q", entry)
		}

		configOverrides[key] = value
	}

	deviceOverrides, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return err
	}

	var op lxd.RemoteOperation
	var start bool

	sourceParentName, sourceSnapName, sourceIsSnap := api.GetParentAndSnapshotName(sourceName)

	if sourceIsSnap {
		if instanceOnly {
			return errors.New("--instance-only can't be passed when the source is a snapshot")
		}

		// Prepare the instance creation request
		args := lxd.InstanceSnapshotCopyArgs{
			Name:  destName,
			Mode:  mode,
			Live:  stateful,
			Start: c.flagStart,
		}

		if c.flagRefresh {
			return errors.New("--refresh can only be used with instances")
		}

		// Copy of a snapshot into a new instance
		entry, _, err := source.GetInstanceSnapshot(sourceParentName, sourceSnapName)
		if err != nil {
			return err
		}

		err = c.applyConfigOverrides(dest, pool, keepVolatile, &entry.Profiles, &entry.Config, &entry.Devices, configOverrides, deviceOverrides)
		if err != nil {
			return err
		}

		// Allow overriding the ephemeral status
		switch ephemeral {
		case 1:
			entry.Ephemeral = true
		case 0:
			entry.Ephemeral = false
		}

		op, err = dest.CopyInstanceSnapshot(source, sourceParentName, *entry, &args)
		if err != nil {
			return err
		}
	} else {
		// Prepare the instance creation request
		args := lxd.InstanceCopyArgs{
			Name:              destName,
			Live:              stateful,
			InstanceOnly:      instanceOnly,
			Mode:              mode,
			Refresh:           c.flagRefresh,
			AllowInconsistent: c.flagAllowInconsistent,
			Start:             c.flagStart,
		}

		// Copy of an instance into a new instance
		entry, _, err := source.GetInstance(sourceName)
		if err != nil {
			return err
		}

		// Only start the instance back up if doing a stateless migration.
		// Its LXD's job to start things back up when receiving a stateful migration.
		// This is when copyInstace is called by the move command and server side move
		// cannot be performed, e.g. when migrating an instance between different LXD servers which are not in the same cluster
		// or when server side move is simply not supported.
		// The server will switch to migration so we cannot simply populate the Start field of the InstanceCopyArgs as this
		// information will get lost during migration and is essentially not received by the target.
		if entry.StatusCode == api.Running && move && !stateful {
			start = true
		}

		err = c.applyConfigOverrides(dest, pool, keepVolatile, &entry.Profiles, &entry.Config, &entry.Devices, configOverrides, deviceOverrides)
		if err != nil {
			return err
		}

		// Traditionally, if instance with snapshots is transferred across projects,
		// the snapshots keep their own profiles.
		// This doesn't work if the snapshot profiles don't exist in the target project.
		// If different profiles are specified for the instance,
		// instruct the server to apply the profiles of the source instance to the snapshots as well.
		if c.flagNoProfiles || c.flagProfile != nil {
			args.OverrideSnapshotProfiles = true
		}

		// Allow overriding the ephemeral status
		switch ephemeral {
		case 1:
			entry.Ephemeral = true
		case 0:
			entry.Ephemeral = false
		}

		op, err = dest.CopyInstance(source, *entry, &args)
		if err != nil {
			return err
		}
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: "Transferring instance: %s",
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for the copy to complete
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	// In case the destination LXD doesn't support instance start on copy,
	// indicate to start the instance manually.
	if c.flagStart && !dest.HasExtension("instance_create_start") {
		start = true
	}

	// Start the instance if needed
	if start {
		req := api.InstanceStatePut{
			Action: string(instancetype.Start),
		}

		op, err := dest.UpdateInstanceState(destName, req, "")
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdCopy) applyConfigOverrides(dest lxd.InstanceServer, poolName string, keepVolatile bool, profiles *[]string, config *map[string]string, devices *map[string]map[string]string, configOverrides map[string]string, deviceOverrides map[string]map[string]string) (err error) {
	if profiles != nil {
		// Overwrite profiles if specified.
		if c.flagProfile != nil {
			*profiles = c.flagProfile
		} else if c.flagNoProfiles {
			*profiles = []string{}
		}
	}

	if config != nil {
		// Strip the volatile keys from source if requested.
		if !keepVolatile {
			for k := range *config {
				if !instancetype.InstanceIncludeWhenCopying(k, true) {
					delete(*config, k)
				}
			}
		}

		// Apply config overrides.
		maps.Copy(*config, configOverrides)

		// Strip the last_state.power key in all cases.
		delete(*config, "volatile.last_state.power")
	}

	if devices != nil {
		// Check to see if any of the devices overrides are for devices that are not yet defined in the
		// local devices and thus are expected to be coming from profiles.
		needProfileExpansion := false
		for deviceName := range deviceOverrides {
			_, isLocalDevice := (*devices)[deviceName]
			if !isLocalDevice {
				needProfileExpansion = true
				break
			}
		}

		profileDevices := make(map[string]map[string]string)

		// If there are device overrides that are expected to be applied to profile devices then perform
		// profile expansion.
		if needProfileExpansion {
			profileDevices, err = getProfileDevices(dest, *profiles)
			if err != nil {
				return err
			}
		}

		// Apply device overrides.
		*devices, err = shared.ApplyDeviceOverrides(*devices, profileDevices, deviceOverrides)
		if err != nil {
			return err
		}

		// Apply storage pool override if specified.
		if poolName != "" {
			rootDiskDeviceKey, _, _ := instancetype.GetRootDiskDevice(*devices)
			if rootDiskDeviceKey != "" {
				// If a root disk device is already defined, just override the pool.
				(*devices)[rootDiskDeviceKey]["pool"] = poolName
			} else {
				// No root disk device defined, add one with the specified pool.
				(*devices)["root"] = map[string]string{
					"type": "disk",
					"path": "/",
					"pool": poolName,
				}
			}
		}
	}

	return nil
}

func (c *cmdCopy) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// For copies, default to non-ephemeral and allow override (move uses -1)
	ephem := 0
	if c.flagEphemeral {
		ephem = 1
	}

	// Parse the mode
	mode := "pull"
	if c.flagMode != "" {
		mode = c.flagMode
	}

	stateful := !c.flagStateless && !c.flagRefresh
	keepVolatile := c.flagRefresh
	instanceOnly := c.flagInstanceOnly

	// If not target name is specified, one will be chosed by the server
	if len(args) < 2 {
		return c.copyInstance(conf, args[0], "", keepVolatile, ephem, stateful, instanceOnly, mode, c.flagStorage, false)
	}

	// Normal copy with a pre-determined name
	return c.copyInstance(conf, args[0], args[1], keepVolatile, ephem, stateful, instanceOnly, mode, c.flagStorage, false)
}
