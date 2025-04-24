package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/termios"
	"github.com/canonical/lxd/shared/units"
)

type volumeColumn struct {
	Name       string
	Data       func(api.StorageVolume, api.StorageVolumeState) string
	NeedsState bool
}

type cmdStorageVolume struct {
	global                *cmdGlobal
	storage               *cmdStorage
	flagDestinationTarget string
}

func parseVolume(defaultType string, name string) (volName string, volType string) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		volName, volType = fields[0], defaultType
	} else if len(fields) == 2 && !slices.Contains([]string{"custom", "image", "container", "virtual-machine"}, fields[0]) {
		volName, volType = name, defaultType
	} else {
		volName, volType = fields[1], fields[0]
	}

	return volName, volType
}

func parseVolumeSnapshot(defaultType string, name string) (volName string, volType string, snapshot string) {
	volName, volType = parseVolume(defaultType, name)

	volName, snapshot, _ = strings.Cut(volName, "/")

	return volName, volType, snapshot
}

func (c *cmdStorageVolume) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("volume")
	cmd.Short = i18n.G("Manage storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage storage volumes

Unless specified through a prefix, all volume operations affect "custom" (user created) volumes.`))

	// Attach
	storageVolumeAttachCmd := cmdStorageVolumeAttach{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeAttachCmd.command())

	// Attach profile
	storageVolumeAttachProfileCmd := cmdStorageVolumeAttachProfile{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeAttachProfileCmd.command())

	// Copy
	storageVolumeCopyCmd := cmdStorageVolumeCopy{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeCopyCmd.command())

	// Create
	storageVolumeCreateCmd := cmdStorageVolumeCreate{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeCreateCmd.command())

	// Delete
	storageVolumeDeleteCmd := cmdStorageVolumeDelete{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDeleteCmd.command())

	// Detach
	storageVolumeDetachCmd := cmdStorageVolumeDetach{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDetachCmd.command())

	// Detach profile
	storageVolumeDetachProfileCmd := cmdStorageVolumeDetachProfile{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDetachProfileCmd.command())

	// Edit
	storageVolumeEditCmd := cmdStorageVolumeEdit{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeEditCmd.command())

	// Export
	storageVolumeExportCmd := cmdStorageVolumeExport{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeExportCmd.command())

	// Get
	storageVolumeGetCmd := cmdStorageVolumeGet{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeGetCmd.command())

	// Import
	storageVolumeImportCmd := cmdStorageVolumeImport{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeImportCmd.command())

	// Info
	storageVolumeInfoCmd := cmdStorageVolumeInfo{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeInfoCmd.command())

	// List
	storageVolumeListCmd := cmdStorageVolumeList{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeListCmd.command())

	// Rename
	storageVolumeRenameCmd := cmdStorageVolumeRename{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeRenameCmd.command())

	// Move
	storageVolumeMoveCmd := cmdStorageVolumeMove{global: c.global, storage: c.storage, storageVolume: c, storageVolumeCopy: &storageVolumeCopyCmd, storageVolumeRename: &storageVolumeRenameCmd}
	cmd.AddCommand(storageVolumeMoveCmd.command())

	// Set
	storageVolumeSetCmd := cmdStorageVolumeSet{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeSetCmd.command())

	// Show
	storageVolumeShowCmd := cmdStorageVolumeShow{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeShowCmd.command())

	// Snapshot
	storageVolumeSnapshotCmd := cmdStorageVolumeSnapshot{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeSnapshotCmd.command())

	// Restore
	storageVolumeRestoreCmd := cmdStorageVolumeRestore{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeRestoreCmd.command())

	// Unset
	storageVolumeUnsetCmd := cmdStorageVolumeUnset{global: c.global, storage: c.storage, storageVolume: c, storageVolumeSet: &storageVolumeSetCmd}
	cmd.AddCommand(storageVolumeUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

func (c *cmdStorageVolume) parseVolumeWithPool(name string) (volumeName string, poolName string) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return fields[0], ""
	}

	return fields[1], fields[0]
}

func cmdAttachArgsAsDevice(client lxd.InstanceServer, poolName string, flagTarget string, args []string) (devName string, device map[string]string, err error) {
	volName, volType, snapshot := parseVolumeSnapshot("custom", args[1])
	if volType != "custom" && volType != "virtual-machine" {
		return "", nil, errors.New(i18n.G(`Only "custom" and "virtual-machine" volumes can be attached to instances`))
	}

	// Attach the volume
	devPath := ""
	if len(args) == 3 {
		devName = args[1]
	} else if len(args) == 4 {
		// Use the provided target.
		if flagTarget != "" && client.IsClustered() {
			client = client.UseTarget(flagTarget)
		}

		vol, _, err := client.GetStoragePoolVolume(poolName, volType, volName)
		if err != nil {
			return "", nil, err
		}

		switch vol.ContentType {
		case "block", "iso":
			devName = args[3]
		case "filesystem":
			// If using a filesystem volume, the path must also be provided as the fourth argument.
			if !strings.HasPrefix(args[3], "/") {
				devPath = path.Join("/", args[3])
			} else {
				devPath = args[3]
			}

			devName = args[1]
		default:
			return "", nil, errors.New(i18n.G("Unsupported content type for attaching to instances"))
		}
	} else if len(args) == 5 {
		// Path and device name have been given to us.
		devName = args[3]
		devPath = args[4]
	}

	// Prepare the instance's device entry
	device = map[string]string{
		"type":   "disk",
		"pool":   poolName,
		"source": volName,
		"path":   devPath,
	}

	// Only specify sourcetype when not the default
	if volType != "custom" {
		device["source.type"] = volType
	}

	if snapshot != "" {
		device["source.snapshot"] = snapshot
	}

	return devName, device, nil
}

// Attach.
type cmdStorageVolumeAttach struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeAttach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("attach", i18n.G("[<remote>:]<pool> [<type>/]<volume>[/<snapshot>] <instance> [<device name>] [<path>]"))
	cmd.Short = i18n.G("Attach new storage volumes to instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach new storage volumes to instances

<type> must be one of "custom" or "virtual-machine"`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 2 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0], "custom")
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpTopLevelResourceInRemote(remote, "instance", toComplete)
	}

	return cmd
}

func (c *cmdStorageVolumeAttach) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 5)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	devName, device, err := cmdAttachArgsAsDevice(resource.server, resource.name, c.storage.flagTarget, args)
	if err != nil {
		return err
	}

	// Add the device to the instance
	err = instanceDeviceAdd(resource.server, args[2], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Attach profile.
type cmdStorageVolumeAttachProfile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeAttachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("attach-profile", i18n.G("[<remote:>]<pool> [<type>/]<volume>[/<snapshot>] <profile> [<device name>] [<path>]"))
	cmd.Short = i18n.G("Attach new storage volumes to profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach new storage volumes to profiles

<type> must be one of "custom" or "virtual-machine"`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 2 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0], "custom")
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpTopLevelResourceInRemote(remote, "profile", toComplete)
	}

	return cmd
}

func (c *cmdStorageVolumeAttachProfile) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 5)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	devName, device, err := cmdAttachArgsAsDevice(resource.server, resource.name, c.storage.flagTarget, args)
	if err != nil {
		return err
	}

	// Add the device to the instance
	err = profileDeviceAdd(resource.server, args[2], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Copy.
type cmdStorageVolumeCopy struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagMode          string
	flagVolumeOnly    bool
	flagTargetProject string
	flagRefresh       bool
}

func (c *cmdStorageVolumeCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<pool>/<volume>[/<snapshot>] [<remote>:]<pool>/<volume>"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy storage volumes`))

	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay.")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.storageVolume.flagDestinationTarget, "destination-target", "", i18n.G("Destination cluster member name")+"``")
	cmd.Flags().BoolVar(&c.flagVolumeOnly, "volume-only", false, i18n.G("Copy the volume without its snapshots"))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Refresh and update the existing storage volume copies"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePoolWithVolume(toComplete)
		}

		if len(args) == 1 {
			completions, directive := c.global.cmpStoragePools(toComplete, true)
			for i, completion := range completions {
				if !strings.Contains(completion, ":") {
					completions[i] = completion + "/"
				}
			}
			return completions, directive
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeCopy) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0], args[1])
	if err != nil {
		return err
	}

	// Source
	srcResource := resources[0]
	if srcResource.name == "" {
		return errors.New(i18n.G("Missing source volume name"))
	}

	srcServer := srcResource.server
	srcPath := srcResource.name

	// If the source server is standalone then --target cannot be provided.
	if c.storage.flagTarget != "" && !srcServer.IsClustered() {
		return errors.New(i18n.G("Cannot set --target when source server is not clustered"))
	}

	// Get source pool and volume name
	srcVolName, srcVolPool := c.storageVolume.parseVolumeWithPool(srcPath)
	if srcVolPool == "" {
		return errors.New(i18n.G("No storage pool for source volume specified"))
	}

	if c.storage.flagTarget != "" {
		srcServer = srcServer.UseTarget(c.storage.flagTarget)
	}

	// Check if requested storage volume exists.
	srcVolParentName, srcVolSnapName, srcIsSnapshot := api.GetParentAndSnapshotName(srcVolName)
	srcVol, _, err := srcServer.GetStoragePoolVolume(srcVolPool, "custom", srcVolParentName)
	if err != nil {
		return err
	}

	if srcIsSnapshot && c.flagVolumeOnly {
		return fmt.Errorf("Cannot set --volume-only when copying a snapshot")
	}

	// If the volume is in local storage, set the target to its location (or provide a helpful error
	// message if the target is incorrect). If the volume is in remote storage (and the source server is clustered) we
	// can use any provided target. Note that for standalone servers, this will set the target to "none".
	if srcVol.Location != "" && srcVol.Location != "none" {
		if c.storage.flagTarget != "" && c.storage.flagTarget != srcVol.Location {
			return fmt.Errorf(i18n.G("Given target %q does not match source volume location %q"), c.storage.flagTarget, srcVol.Location)
		}

		srcServer = srcServer.UseTarget(srcVol.Location)
	} else if c.storage.flagTarget != "" && srcServer.IsClustered() {
		srcServer = srcServer.UseTarget(c.storage.flagTarget)
	}

	// Destination
	dstResource := resources[1]
	dstServer := dstResource.server
	dstPath := dstResource.name

	// We can always set the destination target if the destination server is clustered (for local storage volumes this
	// places the volume on the target member, for remote volumes this does nothing).
	if c.storageVolume.flagDestinationTarget != "" {
		if !dstServer.IsClustered() {
			return errors.New(i18n.G("Cannot set --destination-target when destination server is not clustered"))
		}

		dstServer = dstServer.UseTarget(c.storageVolume.flagDestinationTarget)
	}

	// Get destination pool and volume name
	// TODO: Make is possible to run lxc storage volume copy pool/vol/snap new-pool/new-vol/new-snap
	dstVolName, dstVolPool := c.storageVolume.parseVolumeWithPool(dstPath)
	if dstVolPool == "" {
		return errors.New(i18n.G("No storage pool for target volume specified"))
	}

	// Parse the mode
	mode := "pull"
	if c.flagMode != "" {
		mode = c.flagMode
	}

	var op lxd.RemoteOperation

	// Messages
	opMsg := i18n.G("Copying the storage volume: %s")
	finalMsg := i18n.G("Storage volume copied successfully!")

	if cmd.Name() == "move" {
		opMsg = i18n.G("Moving the storage volume: %s")
		finalMsg = i18n.G("Storage volume moved successfully!")
	}

	// If source is a snapshot get source snapshot volume info and apply to the srcVol.
	if srcIsSnapshot {
		srcVolSnapshot, _, err := srcServer.GetStoragePoolVolumeSnapshot(srcVolPool, "custom", srcVolParentName, srcVolSnapName)
		if err != nil {
			return err
		}

		// Copy info from source snapshot into source volume used for new volume.
		srcVol.Name = srcVolName
		srcVol.Config = srcVolSnapshot.Config
		srcVol.Description = srcVolSnapshot.Description
	}

	if cmd.Name() == "move" && srcServer == dstServer {
		args := &lxd.StoragePoolVolumeMoveArgs{}
		args.Name = dstVolName
		args.Mode = mode
		args.VolumeOnly = false
		args.Project = c.flagTargetProject

		op, err = dstServer.MoveStoragePoolVolume(dstVolPool, srcServer, srcVolPool, *srcVol, args)
		if err != nil {
			return err
		}
	} else {
		args := &lxd.StoragePoolVolumeCopyArgs{}
		args.Name = dstVolName
		args.Mode = mode
		args.VolumeOnly = c.flagVolumeOnly
		args.Refresh = c.flagRefresh

		if c.flagTargetProject != "" {
			dstServer = dstServer.UseProject(c.flagTargetProject)
		}

		op, err = dstServer.CopyStoragePoolVolume(dstVolPool, srcServer, srcVolPool, *srcVol, args)
		if err != nil {
			return err
		}
	}

	// Register progress handler
	progress := cli.ProgressRenderer{
		Format: opMsg,
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	if cmd.Name() == "move" && srcServer != dstServer {
		if srcIsSnapshot {
			_, err = srcServer.DeleteStoragePoolVolumeSnapshot(srcVolPool, srcVol.Type, srcVolParentName, srcVolSnapName)
		} else {
			err = srcServer.DeleteStoragePoolVolume(srcVolPool, srcVol.Type, srcVolName)
		}

		if err != nil {
			progress.Done("")
			return fmt.Errorf("Failed deleting source volume after copy: %w", err)
		}
	}
	progress.Done(finalMsg)

	return nil
}

// Create.
type cmdStorageVolumeCreate struct {
	global          *cmdGlobal
	storage         *cmdStorage
	storageVolume   *cmdStorageVolume
	flagContentType string
}

func (c *cmdStorageVolumeCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<pool> <volume> [key=value...]"))
	cmd.Short = i18n.G("Create new custom storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create new custom storage volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc storage volume create p1 v1

lxc storage volume create p1 v1 < config.yaml
	Create storage volume v1 for pool p1 with configuration from config.yaml.`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagContentType, "type", "filesystem", i18n.G("Content type, block or filesystem")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeCreate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	var volumePut api.StorageVolumePut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &volumePut)
		if err != nil {
			return err
		}
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	// Create the storage volume entry
	vol := api.StorageVolumesPost{
		Name:             volName,
		Type:             volType,
		ContentType:      c.flagContentType,
		StorageVolumePut: volumePut,
	}

	if volumePut.Config == nil {
		vol.Config = map[string]string{}
	}

	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
		}

		vol.Config[entry[0]] = entry[1]
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	err = client.CreateStoragePoolVolume(resource.name, vol)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage volume %s created")+"\n", args[1])
	}

	return nil
}

// Delete.
type cmdStorageVolumeDelete struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<pool> <volume>[/<snapshot>]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	// If a target was specified, delete the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	fields := strings.SplitN(volName, "/", 2)
	if len(fields) == 2 {
		// Delete the snapshot
		op, err := client.DeleteStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1])
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	} else {
		// Delete the volume
		err := client.DeleteStoragePoolVolume(resource.name, volType, volName)
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage volume %s deleted")+"\n", args[1])
	}

	return nil
}

// Detach.
type cmdStorageVolumeDetach struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeDetach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("detach", i18n.G("[<remote>:]<pool> [<type>/]<volume>[/<snapshot>] <instance> [<device name>]"))
	cmd.Short = i18n.G("Detach storage volumes from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Detach storage volumes from instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0], "custom")
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeInstances(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeDetach) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 4)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	// Detach storage volumes
	devName := ""
	if len(args) == 4 {
		devName = args[3]
	}

	// Get the instance entry
	inst, etag, err := resource.server.GetInstance(args[2])
	if err != nil {
		return err
	}

	volName, volType, snapshot := parseVolumeSnapshot("custom", args[1])

	// Find the device
	if devName == "" {
		for n, d := range inst.Devices {
			sourceType := "custom"
			if d["source.type"] != "" {
				sourceType = d["source.type"]
			}

			if d["type"] == "disk" && d["pool"] == resource.name && volType == sourceType && volName == d["source"] && snapshot == d["source.snapshot"] {
				if devName != "" {
					return errors.New(i18n.G("More than one device matches, specify the device name"))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return errors.New(i18n.G("No device found for this storage volume"))
	}

	_, ok := inst.Devices[devName]
	if !ok {
		return errors.New(i18n.G("The specified device doesn't exist"))
	}

	// Remove the device
	delete(inst.Devices, devName)
	op, err := resource.server.UpdateInstance(args[2], inst.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Detach profile.
type cmdStorageVolumeDetachProfile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeDetachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("detach-profile", i18n.G("[<remote:>]<pool> [<type>/]<volume>[/<snapshot>] <profile> [<device name>]"))
	cmd.Short = i18n.G("Detach storage volumes from profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Detach storage volumes from profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0], "custom")
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeProfiles(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeDetachProfile) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 4)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	devName := ""
	if len(args) > 3 {
		devName = args[3]
	}

	// Get the profile entry
	profile, etag, err := resource.server.GetProfile(args[2])
	if err != nil {
		return err
	}

	volName, volType, snapshot := parseVolumeSnapshot("custom", args[1])

	// Find the device
	if devName == "" {
		for n, d := range profile.Devices {
			sourceType := "custom"
			if d["source.type"] != "" {
				sourceType = d["source.type"]
			}

			if d["type"] == "disk" && d["pool"] == resource.name && volType == sourceType && volName == d["source"] && snapshot == d["source.snapshot"] {
				if devName != "" {
					return errors.New(i18n.G("More than one device matches, specify the device name"))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return errors.New(i18n.G("No device found for this storage volume"))
	}

	_, ok := profile.Devices[devName]
	if !ok {
		return errors.New(i18n.G("The specified device doesn't exist"))
	}

	// Remove the device
	delete(profile.Devices, devName)
	err = resource.server.UpdateProfile(args[2], profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Edit.
type cmdStorageVolumeEdit struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<pool> [<type>/]<volume>"))
	cmd.Short = i18n.G("Edit storage volume configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit storage volume configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`Provide the type of the storage volume if it is not custom.
Supported types are custom, image, container and virtual-machine.

lxc storage volume edit [<remote>:]<pool> [<type>/]<volume> < volume.yaml
    Update a storage volume using the content of pool.yaml.`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a storage volume.
### Any line starting with a '# will be ignored.
###
### A storage volume consists of a set of configuration items.
###
### name: vol1
### type: custom
### used_by: []
### config:
###   size: "61203283968"`)
}

func (c *cmdStorageVolumeEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return errors.New(i18n.G("Invalid snapshot name"))
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		if isSnapshot {
			newdata := api.StorageVolumeSnapshotPut{}
			err = yaml.Unmarshal(contents, &newdata)
			if err != nil {
				return err
			}

			err := client.UpdateStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1], newdata, "")
			if err != nil {
				return err
			}

			return nil
		}

		newdata := api.StorageVolumePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return client.UpdateStoragePoolVolume(resource.name, volType, volName, newdata, "")
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	var data []byte
	var snapVol *api.StorageVolumeSnapshot
	var vol *api.StorageVolume
	etag := ""
	if isSnapshot {
		// Extract the current value
		snapVol, etag, err = client.GetStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1])
		if err != nil {
			return err
		}

		data, err = yaml.Marshal(&snapVol)
		if err != nil {
			return err
		}
	} else {
		// Extract the current value
		vol, etag, err = client.GetStoragePoolVolume(resource.name, volType, volName)
		if err != nil {
			return err
		}

		data, err = yaml.Marshal(&vol)
		if err != nil {
			return err
		}
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	if isSnapshot {
		for {
			// Parse the text received from the editor
			newdata := api.StorageVolumeSnapshotPut{}
			err = yaml.Unmarshal(content, &newdata)
			if err == nil {
				err = client.UpdateStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1], newdata, etag)
			}

			// Respawn the editor
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
				fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

				_, err := os.Stdin.Read(make([]byte, 1))
				if err != nil {
					return err
				}

				content, err = shared.TextEditor("", content)
				if err != nil {
					return err
				}

				continue
			}

			break
		}

		return nil
	}

	for {
		// Parse the text received from the editor
		newdata := api.StorageVolume{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateStoragePoolVolume(resource.name, volType, volName, newdata.Writable(), etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Get.
type cmdStorageVolumeGet struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagIsProperty bool
}

func (c *cmdStorageVolumeGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<pool> [<type>/]<volume>[/<snapshot>] <key>"))
	cmd.Short = i18n.G("Get values for storage volume configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for storage volume configuration keys`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`Provide the type of the storage volume if it is not custom.
Supported types are custom, image, container and virtual-machine.

Add the name of the snapshot if type is one of custom, container or virtual-machine.

lxc storage volume get default data size
    Returns the size of a custom volume "data" in pool "default".

lxc storage volume get default virtual-machine/data snapshots.expiry
    Returns the snapshot expiration period for a virtual machine "data" in pool "default".`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a storage volume property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeGet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse input
	volName, volType := parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return errors.New(i18n.G("Invalid snapshot name"))
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	if isSnapshot {
		resp, _, err := client.GetStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1])
		if err != nil {
			return err
		}

		if c.flagIsProperty {
			res, err := getFieldByJsonTag(resp, args[2])
			if err != nil {
				return fmt.Errorf(i18n.G("The property %q does not exist on the storage pool volume snapshot %s/%s: %v"), args[2], fields[0], fields[1], err)
			}

			fmt.Printf("%v\n", res)
		} else {
			v, ok := resp.Config[args[2]]
			if ok {
				fmt.Println(v)
			}
		}

		return nil
	}

	// Get the storage volume entry
	resp, _, err := client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		res, err := getFieldByJsonTag(resp, args[2])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the storage pool volume %q: %v"), args[2], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		v, ok := resp.Config[args[2]]
		if ok {
			fmt.Println(v)
		}
	}

	return nil
}

// Info.
type cmdStorageVolumeInfo struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<pool> [<type>/]<volume>"))
	cmd.Short = i18n.G("Show storage volume state information")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show storage volume state information`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`Provide the type of the storage volume if it is not custom.
Supported types are custom, container and virtual-machine.

lxc storage volume info default data
    Returns state information for a custom volume "data" in pool "default".

lxc storage volume info default virtual-machine/data
    Returns state information for a virtual machine "data" in pool "default".`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeInfo) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing storage pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return errors.New(i18n.G("Invalid snapshot name"))
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// Check if syntax matches a snapshot
	if isSnapshot || volType == "image" {
		return errors.New(i18n.G("Only instance or custom volumes are supported"))
	}

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Get the data.
	vol, _, err := client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	// Instead of failing here if the usage cannot be determined, it is just omitted.
	volState, _ := client.GetStoragePoolVolumeState(resource.name, volType, volName)

	volSnapshots, err := client.GetStoragePoolVolumeSnapshots(resource.name, volType, volName)
	if err != nil {
		return err
	}

	var volBackups []api.StoragePoolVolumeBackup
	if client.HasExtension("custom_volume_backup") && volType == "custom" {
		volBackups, err = client.GetStoragePoolVolumeBackups(resource.name, volName)
		if err != nil {
			return err
		}
	}

	// Render the overview.
	const layout = "2006/01/02 15:04 MST"

	fmt.Printf(i18n.G("Name: %s")+"\n", vol.Name)
	if vol.Description != "" {
		fmt.Printf(i18n.G("Description: %s")+"\n", vol.Description)
	}

	if vol.Type == "" {
		vol.Type = "custom"
	}

	fmt.Printf(i18n.G("Type: %s")+"\n", vol.Type)

	if vol.ContentType == "" {
		vol.ContentType = "filesystem"
	}

	fmt.Printf(i18n.G("Content type: %s")+"\n", vol.ContentType)

	if vol.Location != "" && client.IsClustered() {
		fmt.Printf(i18n.G("Location: %s")+"\n", vol.Location)
	}

	if volState != nil && volState.Usage != nil {
		if volState.Usage.Used > 0 {
			fmt.Printf(i18n.G("Usage: %s")+"\n", units.GetByteSizeStringIEC(int64(volState.Usage.Used), 2))
		}

		if volState.Usage.Total > 0 {
			fmt.Printf(i18n.G("Total: %s")+"\n", units.GetByteSizeStringIEC(int64(volState.Usage.Total), 2))
		}
	}

	if shared.TimeIsSet(vol.CreatedAt) {
		fmt.Printf(i18n.G("Created: %s")+"\n", vol.CreatedAt.Local().Format(layout))
	}

	// List snapshots
	firstSnapshot := true
	if len(volSnapshots) > 0 {
		snapData := [][]string{}

		for _, snap := range volSnapshots {
			if firstSnapshot {
				fmt.Println("\n" + i18n.G("Snapshots:"))
			}

			var row []string

			fields := strings.Split(snap.Name, shared.SnapshotDelimiter)
			row = append(row, fields[len(fields)-1])
			row = append(row, snap.Description)

			if snap.ExpiresAt != nil {
				row = append(row, snap.ExpiresAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			firstSnapshot = false
			snapData = append(snapData, row)
		}

		sort.Sort(cli.SortColumnsNaturally(snapData))
		snapHeader := []string{
			i18n.G("Name"),
			i18n.G("Description"),
			i18n.G("Expires at"),
		}

		_ = cli.RenderTable(cli.TableFormatTable, snapHeader, snapData, volSnapshots)
	}

	// List backups
	firstBackup := true
	if len(volBackups) > 0 {
		backupData := [][]string{}

		for _, backup := range volBackups {
			if firstBackup {
				fmt.Println("\n" + i18n.G("Backups:"))
			}

			var row []string
			row = append(row, backup.Name)

			if shared.TimeIsSet(backup.CreatedAt) {
				row = append(row, backup.CreatedAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			if shared.TimeIsSet(backup.ExpiresAt) {
				row = append(row, backup.ExpiresAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			if backup.VolumeOnly {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			if backup.OptimizedStorage {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			firstBackup = false
			backupData = append(backupData, row)
		}

		backupHeader := []string{
			i18n.G("Name"),
			i18n.G("Taken at"),
			i18n.G("Expires at"),
			i18n.G("Volume Only"),
			i18n.G("Optimized Storage"),
		}

		_ = cli.RenderTable(cli.TableFormatTable, backupHeader, backupData, volBackups)
	}

	return nil
}

// List.
type cmdStorageVolumeList struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagFormat      string
	flagColumns     string
	flagAllProjects bool

	defaultColumns string
}

func (c *cmdStorageVolumeList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:][<pool>] [<filter>...]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volumes")

	c.defaultColumns = "petndcuL"
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", c.defaultColumns, i18n.G("Columns")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("All projects")+"``")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List storage volumes

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Column shorthand chars:
    p - Storage pool name
    c - Content type (filesystem or block)
    d - Description
    e - Project name
    L - Location of the instance (e.g. its cluster member)
    n - Name
    t - Type of volume (custom, image, container or virtual-machine)
    u - Number of references (used by)
    U - Current disk usage`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeList) run(cmd *cobra.Command, args []string) error {
	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	// Process the filters
	filters := []string{}
	if len(args) > 1 {
		filters = append(filters, args[1:]...)
	}

	// Parse resource
	resource, allVolumes, err := c.getResource(args)
	if err != nil {
		return err
	}

	var volumes []api.StorageVolume

	if allVolumes {
		if c.flagAllProjects {
			volumes, err = resource.server.GetVolumesWithFilterAllProjects(filters)
		} else {
			volumes, err = resource.server.GetVolumesWithFilter(filters)
		}
	} else {
		if c.flagAllProjects {
			volumes, err = resource.server.GetStoragePoolVolumesWithFilterAllProjects(resource.name, filters)
		} else {
			volumes, err = resource.server.GetStoragePoolVolumesWithFilter(resource.name, filters)
		}
	}

	if err != nil {
		return err
	}

	// Process the columns
	columns, err := c.parseColumns(resource.server.IsClustered(), allVolumes)
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, vol := range volumes {
		row := []string{}
		for _, column := range columns {
			if column.NeedsState && !shared.IsSnapshot(vol.Name) && vol.Type != "image" {
				state, err := resource.server.GetStoragePoolVolumeState(vol.Pool, vol.Type, vol.Name)
				if err != nil {
					return err
				}

				row = append(row, column.Data(vol, *state))
			} else {
				row = append(row, column.Data(vol, api.StorageVolumeState{}))
			}
		}
		data = append(data, row)
	}

	if len(columns) >= 2 {
		sort.Sort(cli.ByNameAndType(data))
	}

	rawData := make([]*api.StorageVolume, len(volumes))
	for i := range volumes {
		rawData[i] = &volumes[i]
	}

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(c.flagFormat, headers, data, rawData)
}

func (c *cmdStorageVolumeList) getResource(args []string) (*remoteResource, bool, error) {
	var resource remoteResource

	if len(args) == 0 {
		resources, err := c.global.ParseServers("")
		if err != nil {
			return nil, false, err
		}

		resource = resources[0]

		return &resource, true, nil
	}

	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return nil, false, err
	}

	resource = resources[0]

	return &resource, resource.name == "", nil
}

func (c *cmdStorageVolumeList) parseColumns(clustered bool, allVolumes bool) ([]volumeColumn, error) {
	columnsShorthandMap := map[rune]volumeColumn{
		't': {Name: i18n.G("TYPE"), Data: c.typeColumnData},
		'n': {Name: i18n.G("NAME"), Data: c.nameColumnData},
		'd': {Name: i18n.G("DESCRIPTION"), Data: c.descriptionColumnData},
		'c': {Name: i18n.G("CONTENT-TYPE"), Data: c.contentTypeColumnData},
		'u': {Name: i18n.G("USED BY"), Data: c.usedByColumnData},
		'U': {Name: i18n.G("USAGE"), Data: c.usageColumnData, NeedsState: true},
	}

	if clustered {
		columnsShorthandMap['L'] = volumeColumn{Name: i18n.G("LOCATION"), Data: c.locationColumnData}
	} else {
		if c.flagColumns != c.defaultColumns {
			if strings.ContainsAny(c.flagColumns, "L") {
				return nil, errors.New(i18n.G("Can't specify column L when not clustered"))
			}
		}
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "L", "")
	}

	if allVolumes {
		columnsShorthandMap['p'] = volumeColumn{Name: i18n.G("POOL"), Data: c.poolColumnData}
	} else {
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "p", "")
	}

	if c.flagAllProjects {
		columnsShorthandMap['e'] = volumeColumn{Name: i18n.G("PROJECT"), Data: c.projectColumnData}
	} else {
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "e", "")
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []volumeColumn{}
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdStorageVolumeList) poolColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	return vol.Pool
}

func (c *cmdStorageVolumeList) typeColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	if shared.IsSnapshot(vol.Name) {
		return vol.Type + " (snapshot)"
	}

	return vol.Type
}

func (c *cmdStorageVolumeList) nameColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	return vol.Name
}

func (c *cmdStorageVolumeList) descriptionColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	return vol.Description
}

func (c *cmdStorageVolumeList) contentTypeColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	if vol.ContentType == "" {
		return "filesystem"
	}

	return vol.ContentType
}

func (c *cmdStorageVolumeList) usedByColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	return strconv.Itoa(len(vol.UsedBy))
}

func (c *cmdStorageVolumeList) locationColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	return vol.Location
}

func (c *cmdStorageVolumeList) usageColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	if state.Usage != nil {
		return units.GetByteSizeStringIEC(int64(state.Usage.Used), 2)
	}

	return ""
}

func (c *cmdStorageVolumeList) projectColumnData(vol api.StorageVolume, state api.StorageVolumeState) string {
	return vol.Project
}

// Move.
type cmdStorageVolumeMove struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeCopy   *cmdStorageVolumeCopy
	storageVolumeRename *cmdStorageVolumeRename
}

func (c *cmdStorageVolumeMove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("move", i18n.G("[<remote>:]<pool>/<volume> [<remote>:]<pool>/<volume>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move storage volumes between pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Move storage volumes between pools`))

	cmd.Flags().StringVar(&c.storageVolumeCopy.flagMode, "mode", "pull", i18n.G("Transfer mode, one of pull (default), push or relay")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.storageVolume.flagDestinationTarget, "destination-target", "", i18n.G("Destination cluster member name")+"``")
	cmd.Flags().StringVar(&c.storageVolumeCopy.flagTargetProject, "target-project", "", i18n.G("Move to a project different from the source")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePoolWithVolume(toComplete)
		}

		if len(args) == 1 {
			completions, directive := c.global.cmpStoragePools(toComplete, true)
			for i, completion := range completions {
				if !strings.Contains(completion, ":") {
					completions[i] = completion + "/"
				}
			}
			return completions, directive
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeMove) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0], args[1])
	if err != nil {
		return err
	}

	// Source
	srcResource := resources[0]
	if srcResource.name == "" {
		return errors.New(i18n.G("Missing source volume name"))
	}

	srcRemote := srcResource.remote
	srcPath := srcResource.name

	// Get source pool and volume name
	srcVolName, srcVolPool := c.storageVolume.parseVolumeWithPool(srcPath)
	if srcVolPool == "" {
		return errors.New(i18n.G("No storage pool for source volume specified"))
	}

	// Destination
	dstResource := resources[1]
	dstRemote := dstResource.remote
	dstPath := dstResource.name

	// Get target pool and volume name
	dstVolName, dstVolPool := c.storageVolume.parseVolumeWithPool(dstPath)
	if dstVolPool == "" {
		return errors.New(i18n.G("No storage pool for target volume specified"))
	}

	// Rename volume if both remotes and pools of source and target are equal
	// and neither destination cluster member name nor target project are set.
	if srcRemote == dstRemote && srcVolPool == dstVolPool && c.storageVolume.flagDestinationTarget == "" && c.storageVolumeCopy.flagTargetProject == "" {
		var args []string

		if srcRemote != "" {
			args = append(args, srcRemote+":"+srcVolPool)
		} else {
			args = append(args, srcVolPool)
		}

		args = append(args, srcVolName, dstVolName)

		return c.storageVolumeRename.run(cmd, args)
	}

	return c.storageVolumeCopy.run(cmd, args)
}

// Rename.
type cmdStorageVolumeRename struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<pool> <old name>[/<old snapshot name>] <new name>[/<new snapshot name>]"))
	cmd.Short = i18n.G("Rename storage volumes and storage volume snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeRename) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return errors.New(i18n.G("Invalid snapshot name"))
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	if isSnapshot {
		// Create the storage volume entry
		vol := api.StorageVolumeSnapshotPost{}
		dstParentName, dstSnapName, dstIsSnap := api.GetParentAndSnapshotName(args[2])

		if dstParentName != fields[0] {
			return errors.New(i18n.G("Invalid new snapshot name, parent volume must be the same as source"))
		}

		if !dstIsSnap {
			return errors.New(i18n.G("Invalid new snapshot name"))
		}

		vol.Name = dstSnapName

		// If a target member was specified, get the volume with the matching
		// name on that member, if any.
		if c.storage.flagTarget != "" {
			client = client.UseTarget(c.storage.flagTarget)
		}

		if len(fields) != 2 {
			return errors.New(i18n.G("Not a snapshot name"))
		}

		op, err := client.RenameStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1], vol)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}

		fmt.Printf(i18n.G(`Renamed storage volume from "%s" to "%s"`)+"\n", volName, vol.Name)
		return nil
	}

	// Create the storage volume entry
	vol := api.StorageVolumePost{}
	vol.Name = args[2]

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	err = client.RenameStoragePoolVolume(resource.name, volType, volName, vol)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G(`Renamed storage volume from "%s" to "%s"`)+"\n", volName, vol.Name)
	}

	return nil
}

// Set.
type cmdStorageVolumeSet struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagIsProperty bool
}

func (c *cmdStorageVolumeSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<pool> [<type>/]<volume> <key>=<value>..."))
	cmd.Short = i18n.G("Set storage volume configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set storage volume configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc storage volume set [<remote>:]<pool> [<type>/]<volume> <key> <value>`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`Provide the type of the storage volume if it is not custom.
Supported types are custom, image, container and virtual-machine.

lxc storage volume set default data size=1GiB
    Sets the size of a custom volume "data" in pool "default" to 1 GiB.

lxc storage volume set default virtual-machine/data snapshots.expiry=7d
    Sets the snapshot expiration period for a virtual machine "data" in pool "default" to seven days.`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a storage volume property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		// TODO all volume config keys

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Get the values.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	// Parse the input.
	volName, volType := parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return errors.New(i18n.G("Invalid snapshot name"))
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	if isSnapshot {
		if c.flagIsProperty {
			snapVol, etag, err := client.GetStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1])
			if err != nil {
				return err
			}

			writable := snapVol.Writable()
			if cmd.Name() == "unset" {
				for k := range keys {
					err := unsetFieldByJsonTag(&writable, k)
					if err != nil {
						return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
					}
				}
			} else {
				err := unpackKVToWritable(&writable, keys)
				if err != nil {
					return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
				}
			}

			err = client.UpdateStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1], writable, etag)
			if err != nil {
				return err
			}

			return nil
		}

		return errors.New(i18n.G("Snapshots are read-only and can't have their configuration changed"))
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry.
	vol, etag, err := client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	writable := vol.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		// Update the volume config keys.
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	err = client.UpdateStoragePoolVolume(resource.name, vol.Type, vol.Name, writable, etag)
	if err != nil {
		return err
	}

	return nil
}

// Show.
type cmdStorageVolumeShow struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<pool> [<type>/]<volume>[/<snapshot>]"))
	cmd.Short = i18n.G("Show storage volume configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show storage volume configurations`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`Provide the type of the storage volume if it is not custom.
Supported types are custom, image, container and virtual-machine.

Add the name of the snapshot if type is one of custom, container or virtual-machine.

lxc storage volume show default data
    Will show the properties of a custom volume called "data" in the "default" pool.

lxc storage volume show default container/data
    Will show the properties of the filesystem for a container called "data" in the "default" pool.

lxc storage volume show default virtual-machine/data/snap0
    Will show the properties of snapshot "snap0" for a virtual machine called "data" in the "default" pool.`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0], "custom")
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return errors.New(i18n.G("Invalid snapshot name"))
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry
	if isSnapshot {
		vol, _, err := client.GetStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1])
		if err != nil {
			return err
		}

		data, err := yaml.Marshal(&vol)
		if err != nil {
			return err
		}

		fmt.Printf("%s", data)

		return nil
	}

	vol, _, err := client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	sort.Strings(vol.UsedBy)

	data, err := yaml.Marshal(&vol)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdStorageVolumeUnset struct {
	global           *cmdGlobal
	storage          *cmdStorage
	storageVolume    *cmdStorageVolume
	storageVolumeSet *cmdStorageVolumeSet

	flagIsProperty bool
}

func (c *cmdStorageVolumeUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<pool> [<type>/]<volume> <key>"))
	cmd.Short = i18n.G("Unset storage volume configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset storage volume configuration keys`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`Provide the type of the storage volume if it is not custom.
Supported types are custom, image, container and virtual-machine.

lxc storage volume unset default data size
    Removes the size/quota of a custom volume "data" in pool "default".

lxc storage volume unset default virtual-machine/data snapshots.expiry
    Removes the snapshot expiration period for a virtual machine "data" in pool "default".`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a storage volume property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.storageVolumeSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.storageVolumeSet.run(cmd, args)
}

// Snapshot.
type cmdStorageVolumeSnapshot struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagNoExpiry bool
	flagReuse    bool
}

func (c *cmdStorageVolumeSnapshot) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("snapshot", i18n.G("[<remote>:]<pool> <volume> [<snapshot>]"))
	cmd.Short = i18n.G("Snapshot storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Snapshot storage volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc storage volume snapshot default v1 snap0
       Create a snapshot of "v1" in pool "default" called "snap0".

lxc storage volume snapshot default v1 snap0 < config.yaml
       Create a snapshot of "v1" in pool "default" called "snap0" with the configuration from "config.yaml".`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagNoExpiry, "no-expiry", false, i18n.G("Ignore any configured auto-expiry for the storage volume"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the snapshot name already exists, delete and create a new one"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeSnapshot) run(cmd *cobra.Command, args []string) error {
	var stdinData api.StorageVolumeSnapshotPut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Use the provided target.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])
	if volType != "custom" {
		return errors.New(i18n.G("Only \"custom\" volumes can be snapshotted"))
	}

	// Check if the requested storage volume actually exists
	_, _, err = client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	var snapname string
	if len(args) < 3 {
		snapname = ""
	} else {
		snapname = args[2]
	}

	req := api.StorageVolumeSnapshotsPost{
		Name: snapname,
	}

	if c.flagNoExpiry {
		req.ExpiresAt = &time.Time{}
	} else if stdinData.ExpiresAt != nil && !stdinData.ExpiresAt.IsZero() {
		req.ExpiresAt = stdinData.ExpiresAt
	}

	if stdinData.Description != "" {
		req.Description = stdinData.Description
	}

	if c.flagReuse && snapname != "" {
		snap, _, _ := client.GetStoragePoolVolumeSnapshot(resource.name, volType, volName, snapname)
		if snap != nil {
			op, err := client.DeleteStoragePoolVolumeSnapshot(resource.name, volType, volName, snapname)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}
	}

	op, err := client.CreateStoragePoolVolumeSnapshot(resource.name, volType, volName, req)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Restore.
type cmdStorageVolumeRestore struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeRestore) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("restore", i18n.G("[<remote>:]<pool> <volume> <snapshot>"))
	cmd.Short = i18n.G("Restore storage volume snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Restore storage volume snapshots`))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeSnapshots(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeRestore) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Use the provided target.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Check if the requested storage volume actually exists
	_, _, err = client.GetStoragePoolVolume(resource.name, "custom", args[1])
	if err != nil {
		return err
	}

	req := api.StorageVolumePut{
		Restore: args[2],
	}

	_, etag, err := client.GetStoragePoolVolume(resource.name, "custom", args[1])
	if err != nil {
		return err
	}

	return client.UpdateStoragePoolVolume(resource.name, "custom", args[1], req, etag)
}

// Export.
type cmdStorageVolumeExport struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagVolumeOnly           bool
	flagOptimizedStorage     bool
	flagCompressionAlgorithm string
	flagExportVersion        string
}

func (c *cmdStorageVolumeExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<remote>:]<pool> <volume> [<path>]"))
	cmd.Short = i18n.G("Export custom storage volume")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export custom storage volume`))

	cmd.Flags().BoolVar(&c.flagVolumeOnly, "volume-only", false, i18n.G("Export the volume without its snapshots"))
	cmd.Flags().BoolVar(&c.flagOptimizedStorage, "optimized-storage", false,
		i18n.G("Use storage driver optimized format (can only be restored on a similar pool)"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Define a compression algorithm: for backup or none")+"``")
	cmd.Flags().StringVar(&c.flagExportVersion, "export-version", "",
		i18n.G("Use a different metadata format version than the latest one supported by the server (to support imports on older LXD versions)")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdStorageVolumeExport) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Connect to LXD
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Use the provided target.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	volumeOnly := c.flagVolumeOnly

	volName, volType := parseVolume("custom", args[1])
	if volType != "custom" {
		return errors.New(i18n.G("Only \"custom\" volumes can be exported"))
	}

	req := api.StoragePoolVolumeBackupsPost{
		Name:                 "",
		ExpiresAt:            time.Now().Add(24 * time.Hour),
		VolumeOnly:           volumeOnly,
		OptimizedStorage:     c.flagOptimizedStorage,
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	req.Version, err = getExportVersion(d, c.flagExportVersion)
	if err != nil {
		return err
	}

	op, err := d.CreateStoragePoolVolumeBackup(name, volName, req)
	if err != nil {
		return fmt.Errorf("Failed to create storage volume backup: %w", err)
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Backing up storage volume: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait until backup is done
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	err = op.Wait()
	if err != nil {
		return err
	}

	// Get name of backup
	uStr := op.Get().Resources["backups"][0]
	u, err := url.Parse(uStr)
	if err != nil {
		return fmt.Errorf("Invalid URL %q: %w", uStr, err)
	}

	backupName, err := url.PathUnescape(path.Base(u.EscapedPath()))
	if err != nil {
		return fmt.Errorf("Invalid backup name segment in path %q: %w", u.EscapedPath(), err)
	}

	defer func() {
		// Delete backup after we're done
		op, err = d.DeleteStoragePoolVolumeBackup(name, volName, backupName)
		if err == nil {
			_ = op.Wait()
		}
	}()

	var targetName string
	if len(args) > 2 {
		targetName = args[2]
	} else {
		targetName = "backup.tar.gz"
	}

	target, err := os.Create(shared.HostPathFollow(targetName))
	if err != nil {
		return err
	}

	defer func() { _ = target.Close() }()

	// Prepare the download request
	progress = cli.ProgressRenderer{
		Format: i18n.G("Exporting the backup: %s"),
		Quiet:  c.global.flagQuiet,
	}

	backupFileRequest := lxd.BackupFileRequest{
		BackupFile:      io.WriteSeeker(target),
		ProgressHandler: progress.UpdateProgress,
	}

	// Export tarball
	_, err = d.GetStoragePoolVolumeBackupFile(name, volName, backupName, &backupFileRequest)
	if err != nil {
		_ = os.Remove(targetName)
		progress.Done("")
		return fmt.Errorf("Failed to fetch storage volume backup file: %w", err)
	}

	progress.Done(i18n.G("Backup exported successfully!"))
	return nil
}

// Import.
type cmdStorageVolumeImport struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagType string
}

func (c *cmdStorageVolumeImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", i18n.G("[<remote>:]<pool> <backup file> [<volume name>]"))
	cmd.Short = i18n.G("Import custom storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import backups of custom volumes including their snapshots.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc storage volume import default backup0.tar.gz
		Create a new custom volume using backup0.tar.gz as the source.`))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagType, "type", "", i18n.G("Import type, backup or iso (default \"backup\")")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdStorageVolumeImport) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Connect to LXD.
	remote, pool, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Use the provided target.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	file, err := os.Open(shared.HostPathFollow(args[1]))
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	volName := ""
	if len(args) >= 3 {
		volName = args[2]
	}

	if c.flagType == "" {
		// Set type to iso if filename suffix is .iso
		if strings.HasSuffix(file.Name(), ".iso") {
			c.flagType = "iso"
		} else {
			c.flagType = "backup"
		}
	} else {
		// Validate type flag
		if !shared.ValueInSlice(c.flagType, []string{"backup", "iso"}) {
			return fmt.Errorf("Import type needs to be \"backup\" or \"iso\"")
		}
	}

	if c.flagType == "iso" && volName == "" {
		return fmt.Errorf("Importing ISO images requires a volume name to be set")
	}

	progress := cli.ProgressRenderer{
		Format: i18n.G("Importing custom volume: %s"),
		Quiet:  c.global.flagQuiet,
	}

	createArgs := lxd.StoragePoolVolumeBackupArgs{
		BackupFile: &ioprogress.ProgressReader{
			ReadCloser: file,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{Text: strconv.FormatInt(percent, 10) + "% (" + units.GetByteSizeString(speed, 2) + "/s)"})
				},
			},
		},
		Name: volName,
	}

	var op lxd.Operation

	if c.flagType == "iso" {
		op, err = d.CreateStoragePoolVolumeFromISO(pool, createArgs)
	} else {
		op, err = d.CreateStoragePoolVolumeFromBackup(pool, createArgs)
	}

	if err != nil {
		return err
	}

	// Wait for operation to finish.
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}
