package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdCopy struct {
	global *cmdGlobal

	flagNoProfiles    bool
	flagProfile       []string
	flagConfig        []string
	flagDevice        []string
	flagEphemeral     bool
	flagInstanceOnly  bool
	flagMode          string
	flagStateless     bool
	flagStorage       string
	flagTarget        string
	flagTargetProject string
	flagRefresh       bool
}

func (c *cmdCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<source>[/<snapshot>] [[<remote>:]<destination>]"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy instances within or in between LXD servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy instances within or in between LXD servers`))

	cmd.RunE = c.Run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new instance")+"``")
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, i18n.G("Ephemeral instance"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay")+"``")
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false, i18n.G("Copy the instance without its snapshots"))
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful instance stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Create the instance with no profiles applied"))
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Perform an incremental copy"))

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
		return fmt.Errorf(i18n.G("You must specify a source instance name"))
	}

	// Check that a destination instance was specified, if --target is passed.
	if destName == "" && c.flagTarget != "" {
		return fmt.Errorf(i18n.G("You must specify a destination instance name when using --target"))
	}

	// If no destination name was provided, use the same as the source
	if destName == "" && destResource != "" {
		destName = sourceName
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
		return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
	}

	// Parse the config overrides
	configMap := map[string]string{}
	for _, entry := range c.flagConfig {
		if !strings.Contains(entry, "=") {
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
		}

		fields := strings.SplitN(entry, "=", 2)
		configMap[fields[0]] = fields[1]
	}

	// Parse the device overrides
	deviceMap := map[string]map[string]string{}
	for _, entry := range c.flagDevice {
		if !strings.Contains(entry, "=") || !strings.Contains(entry, ",") {
			return fmt.Errorf(i18n.G("Bad syntax, expecting <device>,<key>=<value>: %s"), entry)
		}

		deviceFields := strings.SplitN(entry, ",", 2)
		keyFields := strings.SplitN(deviceFields[1], "=", 2)

		if deviceMap[deviceFields[0]] == nil {
			deviceMap[deviceFields[0]] = map[string]string{}
		}

		deviceMap[deviceFields[0]][keyFields[0]] = keyFields[1]
	}

	var op lxd.RemoteOperation
	var writable api.InstancePut
	var start bool

	if shared.IsSnapshot(sourceName) {
		if instanceOnly {
			return fmt.Errorf(i18n.G("--instance-only can't be passed when the source is a snapshot"))
		}

		// Prepare the instance creation request
		args := lxd.InstanceSnapshotCopyArgs{
			Name: destName,
			Mode: mode,
			Live: stateful,
		}

		if c.flagRefresh {
			return fmt.Errorf(i18n.G("--refresh can only be used with instances"))
		}

		// Copy of a snapshot into a new instance
		srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
		entry, _, err := source.GetInstanceSnapshot(srcFields[0], srcFields[1])
		if err != nil {
			return err
		}

		// Allow adding additional profiles
		if c.flagProfile != nil {
			entry.Profiles = append(entry.Profiles, c.flagProfile...)
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Allow setting additional config keys
		if configMap != nil {
			for key, value := range configMap {
				entry.Config[key] = value
			}
		}

		// Allow setting device overrides
		if deviceMap != nil {
			for k, m := range deviceMap {
				if entry.Devices[k] == nil {
					entry.Devices[k] = m
					continue
				}

				for key, value := range m {
					entry.Devices[k][key] = value
				}
			}
		}

		// Allow overriding the ephemeral status
		if ephemeral == 1 {
			entry.Ephemeral = true
		} else if ephemeral == 0 {
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(entry.Devices)
		if err != nil {
			return err
		}

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
				// Strip all volatile keys
				for k := range entry.Config {
					if k == "volatile.base_image" {
						continue
					}

					if strings.HasPrefix(k, "volatile") {
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
			Name:         destName,
			Live:         stateful,
			InstanceOnly: instanceOnly,
			Mode:         mode,
			Refresh:      c.flagRefresh,
		}

		// Copy of an instance into a new instance
		entry, _, err := source.GetInstance(sourceName)
		if err != nil {
			return err
		}

		if entry.StatusCode == api.Running && move && !stateful {
			start = true
		}

		// Allow adding additional profiles
		if c.flagProfile != nil {
			entry.Profiles = append(entry.Profiles, c.flagProfile...)
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Allow setting additional config keys
		if configMap != nil {
			for key, value := range configMap {
				entry.Config[key] = value
			}
		}

		// Allow setting device overrides
		if deviceMap != nil {
			for k, m := range deviceMap {
				if entry.Devices[k] == nil {
					entry.Devices[k] = m
					continue
				}

				for key, value := range m {
					entry.Devices[k][key] = value
				}
			}
		}

		// Allow overriding the ephemeral status
		if ephemeral == 1 {
			entry.Ephemeral = true
		} else if ephemeral == 0 {
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(entry.Devices)
		if err != nil {
			return err
		}

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
				if k == "volatile.base_image" {
					continue
				}

				if strings.HasPrefix(k, "volatile") {
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
	progress := utils.ProgressRenderer{
		Format: i18n.G("Transferring instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for the copy to complete
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}
	progress.Done("")

	if c.flagRefresh {
		inst, etag, err := dest.GetInstance(destName)
		if err != nil {
			return fmt.Errorf("Failed to refresh target instance '%s': %v", destName, err)
		}

		// Ensure we don't change the target's volatile.idmap.next value.
		writable.Config["volatile.idmap.next"] = inst.Config["volatile.idmap.next"]

		op, err := dest.UpdateInstance(destName, writable, etag)
		if err != nil {
			return err
		}

		// Watch the background operation
		progress := utils.ProgressRenderer{
			Format: i18n.G("Refreshing instance: %s"),
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait for the copy to complete
		err = utils.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}
		progress.Done("")
	}

	// If choosing a random name, show it to the user
	if destResource == "" && c.flagTargetProject == "" {
		// Get the successful operation data
		opInfo, err := op.GetTarget()
		if err != nil {
			return err
		}

		// Extract the list of affected instances
		instances, ok := opInfo.Resources["instances"]
		if !ok || len(instances) != 1 {
			// Extract the list of affected instances using old "containers" field
			instances, ok = opInfo.Resources["containers"]
			if !ok || len(instances) != 1 {
				return fmt.Errorf(i18n.G("Failed to get the new instance name"))
			}
		}

		// Extract the name of the instance
		fields := strings.Split(instances[0], "/")
		fmt.Printf(i18n.G("Instance name is: %s")+"\n", fields[len(fields)-1])
	}

	// Start the instance if needed
	if start {
		req := api.InstanceStatePut{
			Action: string(shared.Start),
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

func (c *cmdCopy) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
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
