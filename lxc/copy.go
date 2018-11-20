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
	flagContainerOnly bool
	flagMode          string
	flagStateless     bool
	flagStorage       string
	flagTarget        string
	flagTargetProject string
	flagRefresh       bool
}

func (c *cmdCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("copy [<remote>:]<source>[/<snapshot>] [[<remote>:]<destination>]")
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy containers within or in between LXD instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy containers within or in between LXD instances`))

	cmd.RunE = c.Run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new container")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new container")+"``")
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, i18n.G("Ephemeral container"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay")+"``")
	cmd.Flags().BoolVar(&c.flagContainerOnly, "container-only", false, i18n.G("Copy the container without its snapshots"))
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful container stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Create the container with no profiles applied"))
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Perform an incremental copy"))

	return cmd
}

func (c *cmdCopy) copyContainer(conf *config.Config, sourceResource string,
	destResource string, keepVolatile bool, ephemeral int, stateful bool,
	containerOnly bool, mode string, pool string) error {
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

	// Make sure we have a container or snapshot name
	if sourceName == "" {
		return fmt.Errorf(i18n.G("You must specify a source container name"))
	}

	// Check that a destination container was specified, if --target is passed.
	if destName == "" && c.flagTarget != "" {
		return fmt.Errorf(i18n.G("You must specify a destination container name when using --target"))
	}

	// If no destination name was provided, use the same as the source
	if destName == "" && destResource != "" {
		destName = sourceName
	}

	// Connect to the source host
	source, err := conf.GetContainerServer(sourceRemote)
	if err != nil {
		return err
	}

	// Connect to the destination host
	var dest lxd.ContainerServer
	if sourceRemote == destRemote {
		// Source and destination are the same
		dest = source
	} else {
		// Destination is different, connect to it
		dest, err = conf.GetContainerServer(destRemote)
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
	var writable api.ContainerPut

	if shared.IsSnapshot(sourceName) {
		if containerOnly {
			return fmt.Errorf(i18n.G("--container-only can't be passed when the source is a snapshot"))
		}

		// Prepare the container creation request
		args := lxd.ContainerSnapshotCopyArgs{
			Name: destName,
			Mode: mode,
			Live: stateful,
		}

		if c.flagRefresh {
			return fmt.Errorf(i18n.G("--refresh can only be used with containers"))
		}

		// Copy of a snapshot into a new container
		srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
		entry, _, err := source.GetContainerSnapshot(srcFields[0], srcFields[1])
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

		// Do the actual copy
		if c.flagTarget != "" {
			dest = dest.UseTarget(c.flagTarget)
		}

		op, err = dest.CopyContainerSnapshot(source, srcFields[0], *entry, &args)
		if err != nil {
			return err
		}
	} else {
		// Prepare the container creation request
		args := lxd.ContainerCopyArgs{
			Name:          destName,
			Live:          stateful,
			ContainerOnly: containerOnly,
			Mode:          mode,
			Refresh:       c.flagRefresh,
		}

		// Copy of a container into a new container
		entry, _, err := source.GetContainer(sourceName)
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

		// Do the actual copy
		if c.flagTarget != "" {
			dest = dest.UseTarget(c.flagTarget)
		}

		op, err = dest.CopyContainer(source, *entry, &args)
		if err != nil {
			return err
		}

		writable = entry.Writable()
	}

	// Watch the background operation
	progress := utils.ProgressRenderer{
		Format: i18n.G("Transferring container: %s"),
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
		_, etag, err := dest.GetContainer(destName)
		if err != nil {
			return fmt.Errorf("Failed to refresh target container '%s': %v", destName, err)
		}

		op, err := dest.UpdateContainer(destName, writable, etag)
		if err != nil {
			return err
		}

		// Watch the background operation
		progress := utils.ProgressRenderer{
			Format: i18n.G("Refreshing container: %s"),
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
	if destResource == "" {
		// Get the successful operation data
		opInfo, err := op.GetTarget()
		if err != nil {
			return err
		}

		// Extract the list of affected containers
		containers, ok := opInfo.Resources["containers"]
		if !ok || len(containers) != 1 {
			return fmt.Errorf(i18n.G("Failed to get the new container name"))
		}

		// Extract the name of the container
		fields := strings.Split(containers[0], "/")
		fmt.Printf(i18n.G("Container name is: %s")+"\n", fields[len(fields)-1])
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

	// If not target name is specified, one will be chosed by the server
	if len(args) < 2 {
		return c.copyContainer(conf, args[0], "", false, ephem,
			stateful, c.flagContainerOnly, mode, c.flagStorage)
	}

	// Normal copy with a pre-determined name
	return c.copyContainer(conf, args[0], args[1], false, ephem,
		stateful, c.flagContainerOnly, mode, c.flagStorage)
}
