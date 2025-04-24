package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
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
}

func (c *cmdCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<source>[/<snapshot>] [[<remote>:]<destination>]"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy instances within or in between LXD servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy instances within or in between LXD servers

Transfer modes (--mode):
 - pull: Target server pulls the data from the source server (source must listen on network)
 - push: Source server pushes the data to the target server (target must listen on network)
 - relay: The CLI connects to both source and server and proxies the data (both source and target must listen on network)

The pull transfer mode is the default as it is compatible with all LXD versions.
`))

	cmd.RunE = c.run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new instance")+"``")
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, i18n.G("Ephemeral instance"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull, push or relay")+"``")
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false, i18n.G("Copy the instance without its snapshots"))
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful instance stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Create the instance with no profiles applied"))
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Perform an incremental copy"))
	cmd.Flags().BoolVar(&c.flagAllowInconsistent, "allow-inconsistent", false, i18n.G("Ignore copy errors for volatile files"))

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
		return errors.New(i18n.G("You must specify a source instance name"))
	}

	// Don't allow refreshing without profiles.
	if c.flagRefresh && c.flagNoProfiles {
		return errors.New(i18n.G("--no-profiles cannot be used with --refresh"))
	}

	// If the instance is being copied to a different remote and no destination name is
	// specified, use the source name with snapshot suffix trimmed (in case a new instance
	// is being created from a snapshot).
	if destName == "" && destResource != "" && c.flagTarget == "" {
		destName = strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)[0]
	}

	// Ensure that a destination name is provided.
	if destName == "" {
		return errors.New(i18n.G("You must specify a destination instance name"))
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

	// Confirm that --target is only used with a cluster
	if c.flagTarget != "" && !dest.IsClustered() {
		return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
	}

	// Parse the config overrides
	configMap := map[string]string{}
	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
		}

		configMap[key] = value
	}

	deviceOverrides, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return err
	}

	var op lxd.RemoteOperation
	var writable api.InstancePut
	var start bool

	if shared.IsSnapshot(sourceName) {
		if instanceOnly {
			return errors.New(i18n.G("--instance-only can't be passed when the source is a snapshot"))
		}

		// Prepare the instance creation request
		args := lxd.InstanceSnapshotCopyArgs{
			Name: destName,
			Mode: mode,
			Live: stateful,
		}

		if c.flagRefresh {
			return errors.New(i18n.G("--refresh can only be used with instances"))
		}

		// Copy of a snapshot into a new instance
		srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
		entry, _, err := source.GetInstanceSnapshot(srcFields[0], srcFields[1])
		if err != nil {
			return err
		}

		// Overwrite profiles.
		if c.flagProfile != nil {
			entry.Profiles = c.flagProfile
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Check to see if any of the overridden devices are for devices that are not yet defined in the
		// local devices (and thus maybe expected to be coming from profiles).
		needProfileExpansion := false
		for deviceName := range deviceOverrides {
			_, isLocalDevice := entry.Devices[deviceName]
			if !isLocalDevice {
				needProfileExpansion = true
				break
			}
		}

		profileDevices := make(map[string]map[string]string)

		// If there are device overrides that are expected to be applied to profile devices then perform
		// profile expansion.
		if needProfileExpansion {
			// If the list of profiles is empty then LXD would apply the default profile on the server side.
			profileDevices, err = getProfileDevices(dest, entry.Profiles)
			if err != nil {
				return err
			}
		}

		// Apply device overrides.
		entry.Devices, err = shared.ApplyDeviceOverrides(profileDevices, entry.Devices, deviceOverrides)
		if err != nil {
			return err
		}

		// Allow setting additional config keys.
		for key, value := range configMap {
			entry.Config[key] = value
		}

		// Allow overriding the ephemeral status
		switch ephemeral {
		case 1:
			entry.Ephemeral = true
		case 0:
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := instancetype.GetRootDiskDevice(entry.Devices)

		if rootDiskDeviceKey != "" && pool != "" {
			entry.Devices[rootDiskDeviceKey]["pool"] = pool
		} else if pool != "" {
			entry.Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool,
			}
		}

		if entry.Config != nil {
			// Strip the last_state.power key in all cases
			delete(entry.Config, "volatile.last_state.power")

			if !keepVolatile {
				for k := range entry.Config {
					if !instancetype.InstanceIncludeWhenCopying(k, true) {
						delete(entry.Config, k)
					}
				}
			}
		}

		// Do the actual copy
		if c.flagTarget != "" {
			dest = dest.UseTarget(c.flagTarget)
		}

		op, err = dest.CopyInstanceSnapshot(source, srcFields[0], *entry, &args)
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
		}

		// Copy of an instance into a new instance
		entry, _, err := source.GetInstance(sourceName)
		if err != nil {
			return err
		}

		// Only start the instance back up if doing a stateless migration.
		// Its LXD's job to start things back up when receiving a stateful migration.
		if entry.StatusCode == api.Running && move && !stateful {
			start = true
		}

		// Overwrite profiles.
		if c.flagProfile != nil {
			entry.Profiles = c.flagProfile
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Traditionally, if instance with snapshots is transferred across projects,
		// the snapshots keep their own profiles.
		// This doesn't work if the snapshot profiles don't exist in the target project.
		// If different profiles are specified for the instance,
		// instruct the server to apply the profiles of the source instance to the snapshots as well.
		if c.flagNoProfiles || c.flagProfile != nil {
			args.OverrideSnapshotProfiles = true
		}

		// Check to see if any of the devices overrides are for devices that are not yet defined in the
		// local devices and thus are expected to be coming from profiles.
		needProfileExpansion := false
		for deviceName := range deviceOverrides {
			_, isLocalDevice := entry.Devices[deviceName]
			if !isLocalDevice {
				needProfileExpansion = true
				break
			}
		}

		profileDevices := make(map[string]map[string]string)

		// If there are device overrides that are expected to be applied to profile devices then perform
		// profile expansion.
		if needProfileExpansion {
			profileDevices, err = getProfileDevices(dest, entry.Profiles)
			if err != nil {
				return err
			}
		}

		// Apply device overrides.
		entry.Devices, err = shared.ApplyDeviceOverrides(entry.Devices, profileDevices, deviceOverrides)
		if err != nil {
			return err
		}

		// Allow setting additional config keys.
		for key, value := range configMap {
			entry.Config[key] = value
		}

		// Allow overriding the ephemeral status
		switch ephemeral {
		case 1:
			entry.Ephemeral = true
		case 0:
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := instancetype.GetRootDiskDevice(entry.Devices)
		if rootDiskDeviceKey != "" && pool != "" {
			entry.Devices[rootDiskDeviceKey]["pool"] = pool
		} else if pool != "" {
			entry.Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool,
			}
		}

		// Strip the volatile keys if requested
		if !keepVolatile {
			for k := range entry.Config {
				if !instancetype.InstanceIncludeWhenCopying(k, true) {
					delete(entry.Config, k)
				}
			}
		}

		if entry.Config != nil {
			// Strip the last_state.power key in all cases
			delete(entry.Config, "volatile.last_state.power")
		}

		// Do the actual copy
		if c.flagTarget != "" {
			dest = dest.UseTarget(c.flagTarget)
		}

		op, err = dest.CopyInstance(source, *entry, &args)
		if err != nil {
			return err
		}

		writable = entry.Writable()
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Transferring instance: %s"),
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

	if c.flagRefresh {
		inst, etag, err := dest.GetInstance(destName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to refresh target instance '%s': %v"), destName, err)
		}

		// Ensure we don't change the target's volatile.idmap.next value.
		if inst.Config["volatile.idmap.next"] != writable.Config["volatile.idmap.next"] {
			writable.Config["volatile.idmap.next"] = inst.Config["volatile.idmap.next"]
		}

		// Ensure we don't change the target's root disk pool.
		srcRootDiskDeviceKey, _, _ := instancetype.GetRootDiskDevice(writable.Devices)
		destRootDiskDeviceKey, destRootDiskDevice, _ := instancetype.GetRootDiskDevice(inst.Devices)
		if srcRootDiskDeviceKey != "" && srcRootDiskDeviceKey == destRootDiskDeviceKey {
			writable.Devices[destRootDiskDeviceKey]["pool"] = destRootDiskDevice["pool"]
		}

		op, err := dest.UpdateInstance(destName, writable, etag)
		if err != nil {
			return err
		}

		// Watch the background operation
		progress := cli.ProgressRenderer{
			Format: i18n.G("Refreshing instance: %s"),
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
