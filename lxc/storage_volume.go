package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdStorageVolume struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageVolume) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("volume")
	cmd.Short = i18n.G("Manage storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage storage volumes

Unless specified through a prefix, all volume operations affect "custom" (user created) volumes.`))

	// Attach
	storageVolumeAttachCmd := cmdStorageVolumeAttach{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeAttachCmd.Command())

	// Attach profile
	storageVolumeAttachProfileCmd := cmdStorageVolumeAttachProfile{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeAttachProfileCmd.Command())

	// Copy
	storageVolumeCopyCmd := cmdStorageVolumeCopy{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeCopyCmd.Command())

	// Create
	storageVolumeCreateCmd := cmdStorageVolumeCreate{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeCreateCmd.Command())

	// Delete
	storageVolumeDeleteCmd := cmdStorageVolumeDelete{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDeleteCmd.Command())

	// Detach
	storageVolumeDetachCmd := cmdStorageVolumeDetach{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDetachCmd.Command())

	// Detach profile
	storageVolumeDetachProfileCmd := cmdStorageVolumeDetachProfile{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDetachProfileCmd.Command())

	// Edit
	storageVolumeEditCmd := cmdStorageVolumeEdit{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeEditCmd.Command())

	// Get
	storageVolumeGetCmd := cmdStorageVolumeGet{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeGetCmd.Command())

	// List
	storageVolumeListCmd := cmdStorageVolumeList{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeListCmd.Command())

	// Move
	storageVolumeMoveCmd := cmdStorageVolumeMove{global: c.global, storage: c.storage, storageVolume: c, storageVolumeCopy: &storageVolumeCopyCmd}
	cmd.AddCommand(storageVolumeMoveCmd.Command())

	// Rename
	storageVolumeRenameCmd := cmdStorageVolumeRename{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeRenameCmd.Command())

	// Set
	storageVolumeSetCmd := cmdStorageVolumeSet{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeSetCmd.Command())

	// Show
	storageVolumeShowCmd := cmdStorageVolumeShow{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeShowCmd.Command())

	// Snapshot
	storageVolumeSnapshotCmd := cmdStorageVolumeSnapshot{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeSnapshotCmd.Command())

	// Restore
	storageVolumeRestoreCmd := cmdStorageVolumeRestore{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeRestoreCmd.Command())

	// Unset
	storageVolumeUnsetCmd := cmdStorageVolumeUnset{global: c.global, storage: c.storage, storageVolume: c, storageVolumeSet: &storageVolumeSetCmd}
	cmd.AddCommand(storageVolumeUnsetCmd.Command())

	return cmd
}

func (c *cmdStorageVolume) parseVolume(defaultType string, name string) (string, string) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return fields[0], defaultType
	} else if len(fields) == 2 && !shared.StringInSlice(fields[0], []string{"custom", "image", "container"}) {
		return name, defaultType
	}

	return fields[1], fields[0]
}

func (c *cmdStorageVolume) parseVolumeWithPool(name string) (string, string) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return fields[0], ""
	}

	return fields[1], fields[0]
}

// Attach
type cmdStorageVolumeAttach struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeAttach) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("attach [<remote>:]<pool> <volume> <container> [<device name>] <path>")
	cmd.Short = i18n.G("Attach new storage volumes to containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach new storage volumes to containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeAttach) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 4, 5)
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	// Attach the volume
	devPath := ""
	devName := ""
	if len(args) == 4 {
		// Only the path has been given to us.
		devPath = args[3]
		devName = args[1]
	} else if len(args) == 5 {
		// Path and device name have been given to us.
		devName = args[3]
		devPath = args[4]
	}

	volName, volType := c.storageVolume.parseVolume("custom", args[1])
	if volType != "custom" {
		return fmt.Errorf(i18n.G("Only \"custom\" volumes can be attached to containers"))
	}

	// Check if the requested storage volume actually exists
	vol, _, err := resource.server.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	// Prepare the container's device entry
	device := map[string]string{
		"type":   "disk",
		"pool":   resource.name,
		"path":   devPath,
		"source": vol.Name,
	}

	// Add the device to the container
	err = containerDeviceAdd(resource.server, args[2], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Attach profile
type cmdStorageVolumeAttachProfile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeAttachProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("attach-profile [<remote:>]<pool> <volume> <profile> [<device name>] <path>")
	cmd.Short = i18n.G("Attach new storage volumes to profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach new storage volumes to profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeAttachProfile) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 4, 5)
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	// Attach the volume
	devPath := ""
	devName := ""
	if len(args) == 4 {
		// Only the path has been given to us.
		devPath = args[3]
		devName = args[1]
	} else if len(args) == 5 {
		// Path and device name have been given to us.
		devName = args[3]
		devPath = args[4]
	}

	volName, volType := c.storageVolume.parseVolume("custom", args[1])
	if volType != "custom" {
		return fmt.Errorf(i18n.G("Only \"custom\" volumes can be attached to containers"))
	}

	// Check if the requested storage volume actually exists
	vol, _, err := resource.server.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	// Prepare the container's device entry
	device := map[string]string{
		"type":   "disk",
		"pool":   resource.name,
		"path":   devPath,
		"source": vol.Name,
	}

	// Add the device to the container
	err = profileDeviceAdd(resource.server, args[2], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Copy
type cmdStorageVolumeCopy struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagMode       string
	flagVolumeOnly bool
}

func (c *cmdStorageVolumeCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("copy <pool>/<volume>[/<snapshot>] <pool>/<volume>")
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy storage volumes`))

	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay.")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVar(&c.flagVolumeOnly, "volume-only", false, i18n.G("Copy the volume without its snapshots"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeCopy) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing source volume name"))
	}

	srcServer := srcResource.server
	srcPath := srcResource.name

	// Destination
	dstResource := resources[1]
	dstServer := dstResource.server
	dstPath := dstResource.name

	// Get source pool and volume name
	srcVolName, srcVolPool := c.storageVolume.parseVolumeWithPool(srcPath)
	if srcVolPool == "" {
		return fmt.Errorf(i18n.G("No storage pool for source volume specified"))
	}

	// Get destination pool and volume name
	// TODO: Make is possible to run lxc storage volume copy pool/vol/snap new-pool/new-vol/new-snap
	dstVolName, dstVolPool := c.storageVolume.parseVolumeWithPool(dstPath)
	if dstVolPool == "" {
		return fmt.Errorf(i18n.G("No storage pool for target volume specified"))
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

	var srcVol *api.StorageVolume

	// Check if requested storage volume exists
	isSnapshot := strings.Contains(srcVolName, "/")

	if isSnapshot {
		fields := strings.SplitN(srcVolName, "/", 2)
		_, _, err = srcServer.GetStoragePoolVolumeSnapshot(srcVolPool,
			"custom", fields[0], fields[1])
		if err != nil {
			return err
		}

		srcVol, _, err = srcServer.GetStoragePoolVolume(srcVolPool, "custom", fields[0])
	} else {
		srcVol, _, err = srcServer.GetStoragePoolVolume(srcVolPool, "custom", srcVolName)
	}
	if err != nil {
		return err
	}

	if cmd.Name() == "move" && srcServer == dstServer {
		args := &lxd.StoragePoolVolumeMoveArgs{}
		args.Name = dstVolName
		args.Mode = mode
		args.VolumeOnly = false

		if isSnapshot {
			srcVol.Name = srcVolName
		}

		op, err = dstServer.MoveStoragePoolVolume(dstVolPool, srcServer, srcVolPool, *srcVol, args)
		if err != nil {
			return err
		}
	} else {
		args := &lxd.StoragePoolVolumeCopyArgs{}
		args.Name = dstVolName
		args.Mode = mode
		args.VolumeOnly = c.flagVolumeOnly

		if isSnapshot {
			srcVol.Name = srcVolName
		}

		op, err = dstServer.CopyStoragePoolVolume(dstVolPool, srcServer, srcVolPool, *srcVol, args)
		if err != nil {
			return err
		}
	}

	// Register progress handler
	progress := utils.ProgressRenderer{
		Format: opMsg,
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	if cmd.Name() == "move" && srcServer != dstServer {
		err := srcServer.DeleteStoragePoolVolume(srcVolPool, srcVol.Type, srcVolName)
		if err != nil {
			progress.Done("")
			return err
		}
	}
	progress.Done(finalMsg)

	return nil
}

// Create
type cmdStorageVolumeCreate struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("create [<remote>:]<pool> <volume> [key=value...]")
	cmd.Short = i18n.G("Create new custom storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create new custom storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeCreate) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	// Create the storage volume entry
	vol := api.StorageVolumesPost{}
	vol.Name = volName
	vol.Type = volType
	vol.Config = map[string]string{}

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

// Delete
type cmdStorageVolumeDelete struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<pool> <volume>[/<snapshot>]")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeDelete) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	// If a target was specified, create the volume on the given member.
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

// Detach
type cmdStorageVolumeDetach struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeDetach) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("detach [<remote>:]<pool> <volume> <container> [<device name>]")
	cmd.Short = i18n.G("Detach storage volumes from containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Detach storage volumes from containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeDetach) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	// Detach storage volumes
	devName := ""
	if len(args) == 4 {
		devName = args[3]
	}

	// Get the container entry
	container, etag, err := resource.server.GetContainer(args[2])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range container.Devices {
			if d["type"] == "disk" && d["pool"] == resource.name && d["source"] == args[1] {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name"))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return fmt.Errorf(i18n.G("No device found for this storage volume"))
	}

	_, ok := container.Devices[devName]
	if !ok {
		return fmt.Errorf(i18n.G("The specified device doesn't exist"))
	}

	// Remove the device
	delete(container.Devices, devName)
	op, err := resource.server.UpdateContainer(args[2], container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Detach profile
type cmdStorageVolumeDetachProfile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeDetachProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("detach-profile [<remote:>]<pool> <volume> <profile> [<device name>]")
	cmd.Short = i18n.G("Detach storage volumes from profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Detach storage volumes from profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeDetachProfile) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
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

	// Find the device
	if devName == "" {
		for n, d := range profile.Devices {
			if d["type"] == "disk" && d["pool"] == resource.name && d["source"] == args[1] {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name"))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return fmt.Errorf(i18n.G("No device found for this storage volume"))
	}

	_, ok := profile.Devices[devName]
	if !ok {
		return fmt.Errorf(i18n.G("The specified device doesn't exist"))
	}

	// Remove the device
	delete(profile.Devices, devName)
	err = resource.server.UpdateProfile(args[2], profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Edit
type cmdStorageVolumeEdit struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<pool> <volume>[/<snapshot>]")
	cmd.Short = i18n.G("Edit storage volume configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit storage volume configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc storage volume edit [<remote>:]<pool> <volume> < volume.yaml
    Update a storage volume using the content of pool.yaml.`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of a storage volume.
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

func (c *cmdStorageVolumeEdit) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return fmt.Errorf("Invalid snapshot name")
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		if isSnapshot && volType == "custom" {
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
	if isSnapshot && volType == "custom" {
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

	if isSnapshot && volType == "custom" {
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
				fmt.Println(i18n.G("Press enter to open the editor again"))

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
			fmt.Println(i18n.G("Press enter to open the editor again"))

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

// Get
type cmdStorageVolumeGet struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("get [<remote>:]<pool> <volume>[/<snapshot>] <key>")
	cmd.Short = i18n.G("Get values for storage volume configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for storage volume configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeGet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return fmt.Errorf("Invalid snapshot name")
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	if isSnapshot && volType == "custom" {
		// Get the storage volume snapshot entry
		resp, _, err := client.GetStoragePoolVolumeSnapshot(resource.name, volType, fields[0], fields[1])
		if err != nil {
			return err
		}

		for k, v := range resp.Config {
			if k == args[2] {
				fmt.Printf("%s\n", v)
			}
		}

		return nil
	}

	// Get the storage volume entry
	resp, _, err := client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	for k, v := range resp.Config {
		if k == args[2] {
			fmt.Printf("%s\n", v)
		}
	}

	return nil
}

// List
type cmdStorageVolumeList struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]<pool>")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List storage volumes`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	volumes, err := resource.server.GetStoragePoolVolumes(resource.name)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, volume := range volumes {
		usedby := strconv.Itoa(len(volume.UsedBy))
		entry := []string{volume.Type, volume.Name, volume.Description, usedby}
		if strings.Contains(volume.Name, "/") {
			entry[0] = fmt.Sprintf("%s (snapshot)", volume.Type)
		}
		if resource.server.IsClustered() {
			entry = append(entry, volume.Location)
		}
		data = append(data, entry)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	header := []string{
		i18n.G("TYPE"),
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}
	if resource.server.IsClustered() {
		header = append(header, i18n.G("LOCATION"))
	}
	table.SetHeader(header)
	sort.Sort(byNameAndType(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Move
type cmdStorageVolumeMove struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeCopy *cmdStorageVolumeCopy
}

func (c *cmdStorageVolumeMove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("move [<pool>/]<volume> [<pool>/]<volume>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move storage volumes between pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Move storage volumes between pools`))

	cmd.Flags().StringVar(&c.storageVolumeCopy.flagMode, "mode", "pull", i18n.G("Transfer mode, one of pull (default), push or relay")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeMove) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	return c.storageVolumeCopy.Run(cmd, args)
}

// Rename
type cmdStorageVolumeRename struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename [<remote>:]<pool> <old name>[/<old snapshot name>] <new name>[/<new snapshot name>]")
	cmd.Short = i18n.G("Rename storage volumes and storage volume snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeRename) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return fmt.Errorf("Invalid snapshot name")
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	if isSnapshot {
		// Create the storage volume entry
		vol := api.StorageVolumeSnapshotPost{}
		vol.Name = args[2]

		// If a target member was specified, get the volume with the matching
		// name on that member, if any.
		if c.storage.flagTarget != "" {
			client = client.UseTarget(c.storage.flagTarget)
		}

		if len(fields) != 2 {
			return fmt.Errorf("Not a snapshot name")
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

// Set
type cmdStorageVolumeSet struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("set [<remote>:]<pool> <volume> <key> <value>")
	cmd.Short = i18n.G("Set storage volume configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set storage volume configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeSet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 4, 4)
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry
	vol, etag, err := resource.server.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	// Get the value
	key := args[2]
	value := args[3]

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
		}
		value = string(buf[:])
	}

	// Update the volume
	vol.Config[key] = value
	err = client.UpdateStoragePoolVolume(resource.name, vol.Type, vol.Name, vol.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Show
type cmdStorageVolumeShow struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<pool> <volume>[/<snapshot>]")
	cmd.Short = i18n.G("Show storage volum configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show storage volume configurations`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc storage volume show default data
    Will show the properties of a custom volume called "data" in the "default" pool.

lxc storage volume show default container/data
    Will show the properties of the filesystem for a container called "data" in the "default" pool.`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeShow) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])

	isSnapshot := false
	fields := strings.Split(volName, "/")
	if len(fields) > 2 {
		return fmt.Errorf("Invalid snapshot name")
	} else if len(fields) > 1 {
		isSnapshot = true
	}

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry
	if isSnapshot && volType == "custom" {
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

// Unset
type cmdStorageVolumeUnset struct {
	global           *cmdGlobal
	storage          *cmdStorage
	storageVolume    *cmdStorageVolume
	storageVolumeSet *cmdStorageVolumeSet
}

func (c *cmdStorageVolumeUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("unset [<remote>:]<pool> <volume> <key>")
	cmd.Short = i18n.G("Unset storage volume configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset storage volume configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeUnset) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	args = append(args, "")
	return c.storageVolumeSet.Run(cmd, args)
}

// Snapshot
type cmdStorageVolumeSnapshot struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeSnapshot) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("snapshot [<remote>:]<pool> <volume> [<snapshot>]")
	cmd.Short = i18n.G("Snapshot storage volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Snapshot storage volumes`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeSnapshot) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Parse the input
	volName, volType := c.storageVolume.parseVolume("custom", args[1])
	if volType != "custom" {
		return fmt.Errorf(i18n.G("Only \"custom\" volumes can be snapshotted"))
	}

	// Check if the requested storage volume actually exists
	_, _, err = resource.server.GetStoragePoolVolume(resource.name, volType, volName)
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

	op, err := client.CreateStoragePoolVolumeSnapshot(resource.name, volType, volName, req)
	if err != nil {
		return err
	}

	return op.Wait()

}

// Restore
type cmdStorageVolumeRestore struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeRestore) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("restore [<remote>:]<pool> <volume> <snapshot>")
	cmd.Short = i18n.G("Restore storage volume snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Restore storage volume snapshots`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageVolumeRestore) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	// Check if the requested storage volume actually exists
	_, _, err = resource.server.GetStoragePoolVolume(resource.name, "custom", args[1])
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
