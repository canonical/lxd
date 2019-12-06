package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	lxc "gopkg.in/lxc/go-lxc.v2"
	cron "gopkg.in/robfig/cron.v2"

	"github.com/flosch/pongo2"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/vmqemu"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/units"
)

func init() {
	// Expose instanceLoadNodeAll to the device package converting the response to a slice of Instances.
	// This is because container types are defined in the main package and are not importable.
	device.InstanceLoadNodeAll = func(s *state.State) ([]device.Instance, error) {
		containers, err := instanceLoadNodeAll(s, instancetype.Any)
		if err != nil {
			return nil, err
		}

		identifiers := []device.Instance{}
		for _, v := range containers {
			identifiers = append(identifiers, device.Instance(v))
		}

		return identifiers, nil
	}

	// Expose instance.LoadByProjectAndName to the device package converting the response to an Instance.
	// This is because container types are defined in the main package and are not importable.
	device.InstanceLoadByProjectAndName = func(s *state.State, project, name string) (device.Instance, error) {
		container, err := instance.LoadByProjectAndName(s, project, name)
		if err != nil {
			return nil, err
		}

		return device.Instance(container), nil
	}

	// Expose instanceValidDevices to the instance package. This is because it relies on
	// containerLXC which cannot be moved out of main package at this time.
	instance.ValidDevices = instanceValidDevices

	// Expose instanceLoad to the instance package. This is because it relies on containerLXC
	// which cannot be moved out of main package this time.
	instance.Load = instanceLoad
}

// Helper functions

func containerValidName(name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		return fmt.Errorf(
			"The character '%s' is reserved for snapshots.",
			shared.SnapshotDelimiter)
	}

	if !shared.ValidHostname(name) {
		return fmt.Errorf("Container name isn't a valid hostname")
	}

	return nil
}

// instanceValidDevices validate instance device configs.
func instanceValidDevices(state *state.State, cluster *db.Cluster, instanceType instancetype.Type, instanceName string, devices deviceConfig.Devices, expanded bool) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	// Create a temporary Instance for use in device validation.
	// Populate it's name, localDevices and expandedDevices properties based on the mode of
	// validation occurring. In non-expanded validation expensive checks should be avoided.
	var inst instance.Instance

	if instanceType == instancetype.Container {
		c := &containerLXC{
			dbType:       instancetype.Container,
			name:         instanceName,
			localDevices: devices.Clone(), // Prevent devices from modifying their config.
		}

		if expanded {
			c.expandedDevices = c.localDevices // Avoid another clone.
		}

		inst = c
	} else if instanceType == instancetype.VM {
		instArgs := db.InstanceArgs{
			Name:    instanceName,
			Type:    instancetype.VM,
			Devices: devices.Clone(), // Prevent devices from modifying their config.
		}

		if expanded {
			// The devices being validated are already expanded, so just use the same
			// devices clone as we used for the main devices config.
			inst = vmqemu.Instantiate(state, instArgs, instArgs.Devices)
		} else {
			inst = vmqemu.Instantiate(state, instArgs, nil)
		}
	} else {
		return fmt.Errorf("Invalid instance type")
	}

	// Check each device individually using the device package.
	for name, config := range devices {
		_, err := device.New(inst, state, name, config, nil, nil)
		if err != nil {
			return err
		}

	}

	// Check we have a root disk if in expanded validation mode.
	if expanded {
		_, _, err := shared.GetRootDiskDevice(devices.CloneNative())
		if err != nil {
			return errors.Wrap(err, "Detect root disk device")
		}
	}

	return nil
}

// instanceCreateAsEmpty creates an empty instance.
func instanceCreateAsEmpty(d *Daemon, args db.InstanceArgs) (instance.Instance, error) {
	// Create the instance record.
	inst, err := instanceCreateInternal(d.State(), args)
	if err != nil {
		return nil, err
	}

	revert := true
	defer func() {
		if !revert {
			return
		}

		inst.Delete()
	}()

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented {
		if err != nil {
			return nil, errors.Wrap(err, "Load instance storage pool")
		}

		err = pool.CreateInstance(inst, nil)
		if err != nil {
			return nil, errors.Wrap(err, "Create instance")
		}
	} else if inst.Type() == instancetype.Container {
		ct := inst.(*containerLXC)

		// Now create the empty storage.
		err = ct.Storage().ContainerCreate(inst)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("Instance type not supported")
	}

	// Apply any post-storage configuration.
	err = instanceConfigureInternal(d.State(), inst)
	if err != nil {
		return nil, err
	}

	revert = false
	return inst, nil
}

// instanceCreateFromBackup imports a backup file to restore an instance. Because the backup file
// is unpacked and restored onto the storage device before the instance is created in the database
// it is necessary to return two functions; a post hook that can be run once the instance has been
// created in the database to run any storage layer finalisations, and a revert hook that can be
// run if the instance database load process fails that will remove anything created thus far.
func instanceCreateFromBackup(s *state.State, info backup.Info, srcData io.ReadSeeker) (func(instance.Instance) error, func(), error) {
	// Define hook functions that will be returned to caller.
	var postHook func(instance.Instance) error
	var revertHook func()

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(s, info.Pool)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return nil, nil, err
		}

		postHook, revertHook, err = pool.CreateInstanceFromBackup(info, srcData, nil)
		if err != nil {
			return nil, nil, err
		}
	} else { // Fallback to old storage layer.

		// Find the compression algorithm.
		srcData.Seek(0, 0)
		tarArgs, _, _, err := shared.DetectCompressionFile(srcData)
		if err != nil {
			return nil, nil, err
		}

		pool, err := storagePoolInit(s, info.Pool)
		if err != nil {
			return nil, nil, err
		}

		// Unpack tarball from the source tar stream.
		srcData.Seek(0, 0)
		err = pool.ContainerBackupLoad(info, srcData, tarArgs)
		if err != nil {
			return nil, nil, err
		}

		// Update pool information in the backup.yaml file.
		// Requires the volume and snapshots be mounted from pool.ContainerBackupLoad().
		mountPath := shared.VarPath("storage-pools", info.Pool, "containers", project.Prefix(info.Project, info.Name))
		err = backup.UpdateInstanceConfigStoragePool(s.Cluster, info, mountPath)
		if err != nil {
			return nil, nil, err
		}

		// Set revert function to remove the files created so far.
		revertHook = func() {
			// Create a temporary container struct (because the container DB record
			// hasn't been imported yet) for use with storage layer.
			ctTmp := &containerLXC{name: info.Name, project: info.Project}
			pool.ContainerDelete(ctTmp)
		}

		postHook = func(inst instance.Instance) error {
			_, err = inst.StorageStop()
			if err != nil {
				return errors.Wrap(err, "Stop storage pool")
			}

			return nil
		}
	}

	return postHook, revertHook, nil
}

func containerCreateEmptySnapshot(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	// Create the snapshot
	c, err := instanceCreateInternal(s, args)
	if err != nil {
		return nil, err
	}

	if c.Type() != instancetype.Container {
		return nil, fmt.Errorf("Instance type must be container")
	}

	ct := c.(*containerLXC)

	// Now create the empty snapshot
	err = ct.Storage().ContainerSnapshotCreateEmpty(c)
	if err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

// instanceCreateFromImage creates an instance from a rootfs image.
func instanceCreateFromImage(d *Daemon, args db.InstanceArgs, hash string, op *operations.Operation) (instance.Instance, error) {
	s := d.State()

	// Get the image properties.
	_, img, err := s.Cluster.ImageGet(args.Project, hash, false, false)
	if err != nil {
		return nil, errors.Wrapf(err, "Fetch image %s from database", hash)
	}

	// Validate the type of the image matches the type of the instance.
	imgType, err := instancetype.New(img.Type)
	if err != nil {
		return nil, err
	}

	if imgType != args.Type {
		return nil, fmt.Errorf("Requested image's type '%s' doesn't match instance type '%s'", imgType, args.Type)
	}

	// Check if the image is available locally or it's on another node.
	nodeAddress, err := s.Cluster.ImageLocate(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "Locate image %s in the cluster", hash)
	}
	if nodeAddress != "" {
		// The image is available from another node, let's try to import it.
		logger.Debugf("Transferring image %s from node %s", hash, nodeAddress)
		client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), false)
		if err != nil {
			return nil, err
		}

		client = client.UseProject(args.Project)

		err = imageImportFromNode(filepath.Join(d.os.VarDir, "images"), client, hash)
		if err != nil {
			return nil, err
		}

		err = d.cluster.ImageAssociateNode(args.Project, hash)
		if err != nil {
			return nil, err
		}
	}

	// Set the "image.*" keys.
	if img.Properties != nil {
		for k, v := range img.Properties {
			args.Config[fmt.Sprintf("image.%s", k)] = v
		}
	}

	// Set the BaseImage field (regardless of previous value).
	args.BaseImage = hash

	// Create the instance.
	inst, err := instanceCreateInternal(s, args)
	if err != nil {
		return nil, errors.Wrap(err, "Create instance")
	}

	revert := true
	defer func() {
		if !revert {
			return
		}

		inst.Delete()
	}()

	err = s.Cluster.ImageLastAccessUpdate(hash, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented {
		if err != nil {
			return nil, errors.Wrap(err, "Load instance storage pool")
		}

		err = pool.CreateInstanceFromImage(inst, hash, op)
		if err != nil {
			return nil, errors.Wrap(err, "Create instance from image")
		}
	} else if inst.Type() == instancetype.Container {
		metadata := make(map[string]interface{})
		var tracker *ioprogress.ProgressTracker
		if op != nil {
			tracker = &ioprogress.ProgressTracker{
				Handler: func(percent, speed int64) {
					shared.SetProgressMetadata(metadata, "create_instance_from_image_unpack", "Unpack", percent, 0, speed)
					op.UpdateMetadata(metadata)
				}}
		}

		// Now create the storage from an image.
		ct := inst.(*containerLXC)
		err = ct.Storage().ContainerCreateFromImage(inst, hash, tracker)
		if err != nil {
			return nil, errors.Wrap(err, "Create instance from image")
		}
	} else {
		return nil, fmt.Errorf("Instance type not supported")
	}

	// Apply any post-storage configuration.
	err = instanceConfigureInternal(d.State(), inst)
	if err != nil {
		return nil, errors.Wrap(err, "Configure instance")
	}

	revert = false
	return inst, nil
}

func instanceCreateAsCopy(s *state.State, args db.InstanceArgs, sourceInst instance.Instance, instanceOnly bool, refresh bool, op *operations.Operation) (instance.Instance, error) {
	var inst, revertInst instance.Instance
	var err error

	defer func() {
		if revertInst == nil {
			return
		}

		revertInst.Delete()
	}()

	if refresh {
		// Load the target instance.
		inst, err = instance.LoadByProjectAndName(s, args.Project, args.Name)
		if err != nil {
			refresh = false // Instance doesn't exist, so switch to copy mode.
		}

		if inst.IsRunning() {
			return nil, fmt.Errorf("Cannot refresh a running instance")
		}
	}

	// If we are not in refresh mode, then create a new instance as we are in copy mode.
	if !refresh {
		// Create the instance.
		inst, err = instanceCreateInternal(s, args)
		if err != nil {
			return nil, err
		}
		revertInst = inst
	}

	// At this point we have already figured out the parent container's root disk device so we
	// can simply retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := inst.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices.CloneNative())
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	snapList := []*instance.Instance{}
	var snapshots []instance.Instance

	if !instanceOnly {
		if refresh {
			// Compare snapshots.
			syncSnapshots, deleteSnapshots, err := instance.CompareSnapshots(sourceInst, inst)
			if err != nil {
				return nil, err
			}

			// Delete extra snapshots first.
			for _, snap := range deleteSnapshots {
				err := snap.Delete()
				if err != nil {
					return nil, err
				}
			}

			// Only care about the snapshots that need updating.
			snapshots = syncSnapshots
		} else {
			// Get snapshots of source instance.
			snapshots, err = sourceInst.Snapshots()
			if err != nil {
				return nil, err
			}
		}

		for _, srcSnap := range snapshots {
			fields := strings.SplitN(srcSnap.Name(), shared.SnapshotDelimiter, 2)

			// Ensure that snapshot and parent instance have the
			// same storage pool in their local root disk device.
			// If the root disk device for the snapshot comes from a
			// profile on the new instance as well we don't need to
			// do anything.
			snapDevices := srcSnap.LocalDevices()
			if snapDevices != nil {
				snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapDevices.CloneNative())
				if snapLocalRootDiskDeviceKey != "" {
					snapDevices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
				} else {
					snapDevices["root"] = map[string]string{
						"type": "disk",
						"path": "/",
						"pool": parentStoragePool,
					}
				}
			}

			newSnapName := fmt.Sprintf("%s/%s", inst.Name(), fields[1])
			snapInstArgs := db.InstanceArgs{
				Architecture: srcSnap.Architecture(),
				Config:       srcSnap.LocalConfig(),
				Type:         sourceInst.Type(),
				Snapshot:     true,
				Devices:      snapDevices,
				Description:  srcSnap.Description(),
				Ephemeral:    srcSnap.IsEphemeral(),
				Name:         newSnapName,
				Profiles:     srcSnap.Profiles(),
				Project:      args.Project,
			}

			// Create the snapshots.
			snapInst, err := instanceCreateInternal(s, snapInstArgs)
			if err != nil {
				return nil, err
			}

			// Set snapshot creation date to that of the source snapshot.
			err = s.Cluster.InstanceSnapshotCreationUpdate(snapInst.ID(), srcSnap.CreationDate())
			if err != nil {
				return nil, err
			}

			snapList = append(snapList, &snapInst)
		}
	}

	// Check if we can load new storage layer for both target and source pool driver types.
	pool, err := storagePools.GetPoolByInstance(s, inst)
	_, srcPoolErr := storagePools.GetPoolByInstance(s, sourceInst)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented && srcPoolErr != storageDrivers.ErrUnknownDriver && srcPoolErr != storageDrivers.ErrNotImplemented {
		if err != nil {
			return nil, errors.Wrap(err, "Load instance storage pool")
		}

		if refresh {
			err = pool.RefreshInstance(inst, sourceInst, snapshots, op)
			if err != nil {
				return nil, errors.Wrap(err, "Refresh instance")
			}
		} else {
			err = pool.CreateInstanceFromCopy(inst, sourceInst, !instanceOnly, op)
			if err != nil {
				return nil, errors.Wrap(err, "Create instance from copy")
			}
		}
	} else if inst.Type() == instancetype.Container {
		ct := inst.(*containerLXC)

		if refresh {
			err = ct.Storage().ContainerRefresh(inst, sourceInst, snapshots)
			if err != nil {
				return nil, err
			}
		} else {
			err = ct.Storage().ContainerCopy(inst, sourceInst, instanceOnly)
			if err != nil {
				return nil, err
			}
		}
	} else {
		return nil, fmt.Errorf("Instance type not supported")
	}

	// Apply any post-storage configuration.
	err = instanceConfigureInternal(s, inst)
	if err != nil {
		return nil, err
	}

	if !instanceOnly {
		for _, snap := range snapList {
			// Apply any post-storage configuration.
			err = instanceConfigureInternal(s, *snap)
			if err != nil {
				return nil, err
			}
		}
	}

	revertInst = nil
	return inst, nil
}

func instanceCreateAsSnapshot(s *state.State, args db.InstanceArgs, sourceInstance instance.Instance, op *operations.Operation) (instance.Instance, error) {
	if sourceInstance.Type() != instancetype.Container {
		return nil, fmt.Errorf("Instance is not container type")
	}

	if sourceInstance.Type() != args.Type {
		return nil, fmt.Errorf("Source instance and snapshot instance types do not match")
	}

	// Deal with state.
	if args.Stateful {
		if !sourceInstance.IsRunning() {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. The instance isn't running")
		}

		_, err := exec.LookPath("criu")
		if err != nil {
			return nil, fmt.Errorf("Unable to create a stateful snapshot. CRIU isn't installed")
		}

		stateDir := sourceInstance.StatePath()
		err = os.MkdirAll(stateDir, 0700)
		if err != nil {
			return nil, err
		}

		/* TODO: ideally we would freeze here and unfreeze below after
		 * we've copied the filesystem, to make sure there are no
		 * changes by the container while snapshotting. Unfortunately
		 * there is abug in CRIU where it doesn't leave the container
		 * in the same state it found it w.r.t. freezing, i.e. CRIU
		 * freezes too, and then /always/ thaws, even if the container
		 * was frozen. Until that's fixed, all calls to Unfreeze()
		 * after snapshotting will fail.
		 */

		criuMigrationArgs := CriuMigrationArgs{
			cmd:          lxc.MIGRATE_DUMP,
			stateDir:     stateDir,
			function:     "snapshot",
			stop:         false,
			actionScript: false,
			dumpDir:      "",
			preDumpDir:   "",
		}

		c := sourceInstance.(*containerLXC)
		err = c.Migrate(&criuMigrationArgs)
		if err != nil {
			os.RemoveAll(sourceInstance.StatePath())
			return nil, err
		}
	}

	// Create the snapshot.
	inst, err := instanceCreateInternal(s, args)
	if err != nil {
		return nil, err
	}

	revert := true
	defer func() {
		if !revert {
			return
		}

		inst.Delete()
	}()

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByInstance(s, inst)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented {
		if err != nil {
			return nil, err
		}

		err = pool.CreateInstanceSnapshot(inst, sourceInstance, op)
		if err != nil {
			return nil, errors.Wrap(err, "Create instance snapshot")
		}

		// Mount volume for backup.yaml writing.
		ourStart, err := pool.MountInstance(sourceInstance, op)
		if err != nil {
			return nil, errors.Wrap(err, "Create instance snapshot (mount source)")
		}
		if ourStart {
			defer pool.UnmountInstance(sourceInstance, op)
		}
	} else if inst.Type() == instancetype.Container {
		ct := sourceInstance.(*containerLXC)
		err = ct.Storage().ContainerSnapshotCreate(inst, sourceInstance)
		if err != nil {
			return nil, err
		}

		// Mount volume for backup.yaml writing.
		ourStart, err := sourceInstance.StorageStart()
		if err != nil {
			return nil, err
		}
		if ourStart {
			defer sourceInstance.StorageStop()
		}

	} else {
		return nil, fmt.Errorf("Instance type not supported")
	}

	// Attempt to update backup.yaml for instance.
	err = instance.WriteBackupFile(s, sourceInstance)
	if err != nil {
		return nil, err
	}

	// Once we're done, remove the state directory.
	if args.Stateful {
		os.RemoveAll(sourceInstance.StatePath())
	}

	s.Events.SendLifecycle(sourceInstance.Project(), "container-snapshot-created",
		fmt.Sprintf("/1.0/containers/%s", sourceInstance.Name()),
		map[string]interface{}{
			"snapshot_name": args.Name,
		})

	revert = false
	return inst, nil
}

// instanceCreateInternal creates an instance record and storage volume record in the database.
func instanceCreateInternal(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	// Set default values.
	if args.Project == "" {
		args.Project = "default"
	}

	if args.Profiles == nil {
		args.Profiles = []string{"default"}
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
	}

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = s.OS.Architectures[0]
	}

	// Validate container name if not snapshot (as snapshots use disallowed / char in names).
	if !args.Snapshot {
		err := containerValidName(args.Name)
		if err != nil {
			return nil, err
		}

		// Unset expiry date since containers don't expire.
		args.ExpiryDate = time.Time{}
	}

	// Validate container config.
	err := instance.ValidConfig(s.OS, args.Config, false, false)
	if err != nil {
		return nil, err
	}

	// Validate container devices with the supplied container name and devices.
	err = instanceValidDevices(s, s.Cluster, args.Type, args.Name, args.Devices, false)
	if err != nil {
		return nil, errors.Wrap(err, "Invalid devices")
	}

	// Validate architecture.
	_, err = osarch.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	if !shared.IntInSlice(args.Architecture, s.OS.Architectures) {
		return nil, fmt.Errorf("Requested architecture isn't supported by this host")
	}

	// Validate profiles.
	profiles, err := s.Cluster.Profiles(args.Project)
	if err != nil {
		return nil, err
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile '%s' doesn't exist", profile)
		}

		if shared.StringInSlice(profile, checkedProfiles) {
			return nil, fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile)
	}

	if args.CreationDate.IsZero() {
		args.CreationDate = time.Now().UTC()
	}

	if args.LastUsedDate.IsZero() {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	var dbInst db.Instance

	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		node, err := tx.NodeName()
		if err != nil {
			return err
		}

		// TODO: this check should probably be performed by the db package itself.
		exists, err := tx.ProjectExists(args.Project)
		if err != nil {
			return errors.Wrapf(err, "Check if project %q exists", args.Project)
		}
		if !exists {
			return fmt.Errorf("Project %q does not exist", args.Project)
		}

		if args.Snapshot {
			parts := strings.SplitN(args.Name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]
			instance, err := tx.InstanceGet(args.Project, instanceName)
			if err != nil {
				return fmt.Errorf("Get instance %q in project %q", instanceName, args.Project)
			}
			snapshot := db.InstanceSnapshot{
				Project:      args.Project,
				Instance:     instanceName,
				Name:         snapshotName,
				CreationDate: args.CreationDate,
				Stateful:     args.Stateful,
				Description:  args.Description,
				Config:       args.Config,
				Devices:      args.Devices.CloneNative(),
				ExpiryDate:   args.ExpiryDate,
			}
			_, err = tx.InstanceSnapshotCreate(snapshot)
			if err != nil {
				return errors.Wrap(err, "Add snapshot info to the database")
			}

			// Read back the snapshot, to get ID and creation time.
			s, err := tx.InstanceSnapshotGet(args.Project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrap(err, "Fetch created snapshot from the database")
			}

			dbInst = db.InstanceSnapshotToInstance(instance, s)

			return nil
		}

		// Create the instance entry.
		dbInst = db.Instance{
			Project:      args.Project,
			Name:         args.Name,
			Node:         node,
			Type:         args.Type,
			Snapshot:     args.Snapshot,
			Architecture: args.Architecture,
			Ephemeral:    args.Ephemeral,
			CreationDate: args.CreationDate,
			Stateful:     args.Stateful,
			LastUseDate:  args.LastUsedDate,
			Description:  args.Description,
			Config:       args.Config,
			Devices:      args.Devices.CloneNative(),
			Profiles:     args.Profiles,
			ExpiryDate:   args.ExpiryDate,
		}

		_, err = tx.InstanceCreate(dbInst)
		if err != nil {
			return errors.Wrap(err, "Add instance info to the database")
		}

		// Read back the instance, to get ID and creation time.
		dbRow, err := tx.InstanceGet(args.Project, args.Name)
		if err != nil {
			return errors.Wrap(err, "Fetch created instance from the database")
		}

		dbInst = *dbRow

		if dbInst.ID < 1 {
			return errors.Wrapf(err, "Unexpected instance database ID %d", dbInst.ID)
		}

		return nil
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			thing := "Instance"
			if shared.IsSnapshot(args.Name) {
				thing = "Snapshot"
			}
			return nil, fmt.Errorf("%s '%s' already exists", thing, args.Name)
		}
		return nil, err
	}

	revert := true
	defer func() {
		if !revert {
			return
		}

		s.Cluster.InstanceRemove(dbInst.Project, dbInst.Name)
	}()

	// Wipe any existing log for this instance name.
	os.RemoveAll(shared.LogPath(args.Name))

	args = db.ContainerToArgs(&dbInst)

	var inst instance.Instance

	if args.Type == instancetype.Container {
		inst, err = containerLXCCreate(s, args)
	} else if args.Type == instancetype.VM {
		inst, err = vmqemu.Create(s, args)
	} else {
		return nil, fmt.Errorf("Instance type invalid")
	}

	if err != nil {
		return nil, errors.Wrap(err, "Create instance")
	}

	revert = false
	return inst, nil
}

// instanceConfigureInternal applies quota set in volatile "apply_quota" and writes a backup file.
func instanceConfigureInternal(state *state.State, c instance.Instance) error {
	// Find the root device
	rootDiskDeviceKey, rootDiskDevice, err := shared.GetRootDiskDevice(c.ExpandedDevices().CloneNative())
	if err != nil {
		return err
	}

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByInstance(state, c)
	if err != storageDrivers.ErrUnknownDriver && err != storageDrivers.ErrNotImplemented {
		if err != nil {
			return errors.Wrap(err, "Load instance storage pool")
		}

		if rootDiskDevice["size"] != "" {
			err = pool.SetInstanceQuota(c, rootDiskDevice["size"], nil)

			// If the storage driver can't set the quota now, store in volatile.
			if err == storagePools.ErrRunningQuotaResizeNotSupported {
				err = c.VolatileSet(map[string]string{fmt.Sprintf("volatile.%s.apply_quota", rootDiskDeviceKey): rootDiskDevice["size"]})
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
	} else if c.Type() == instancetype.Container {
		ourStart, err := c.StorageStart()
		if err != nil {
			return err
		}

		ct := c.(*containerLXC)

		// handle quota: at this point, storage is guaranteed to be ready.
		storage := ct.Storage()
		if rootDiskDevice["size"] != "" {
			storageTypeName := storage.GetStorageTypeName()
			if (storageTypeName == "lvm" || storageTypeName == "ceph") && c.IsRunning() {
				err = c.VolatileSet(map[string]string{fmt.Sprintf("volatile.%s.apply_quota", rootDiskDeviceKey): rootDiskDevice["size"]})
				if err != nil {
					return err
				}
			} else {
				size, err := units.ParseByteSizeString(rootDiskDevice["size"])
				if err != nil {
					return err
				}

				err = storage.StorageEntitySetQuota(storagePoolVolumeTypeContainer, size, c)
				if err != nil {
					return err
				}
			}
		}

		if ourStart {
			defer c.StorageStop()
		}
	} else {
		return fmt.Errorf("Instance type not supported")
	}

	err = instance.WriteBackupFile(state, c)
	if err != nil {
		return err
	}

	return nil
}

func instanceLoadByProject(s *state.State, project string) ([]instance.Instance, error) {
	// Get all the containers
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.InstanceFilter{
			Project: project,
			Type:    instancetype.Any,
		}
		var err error
		cts, err = tx.InstanceList(filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

// Load all instances across all projects.
func instanceLoadFromAllProjects(s *state.State) ([]instance.Instance, error) {
	var projects []string

	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		projects, err = tx.ProjectNames()
		return err
	})
	if err != nil {
		return nil, err
	}

	instances := []instance.Instance{}
	for _, project := range projects {
		projectInstances, err := instanceLoadByProject(s, project)
		if err != nil {
			return nil, errors.Wrapf(nil, "Load instances in project %s", project)
		}
		instances = append(instances, projectInstances...)
	}

	return instances, nil
}

// Legacy interface.
func instanceLoadAll(s *state.State) ([]instance.Instance, error) {
	return instanceLoadByProject(s, "default")
}

// Load all instances of this nodes.
func instanceLoadNodeAll(s *state.State, instanceType instancetype.Type) ([]instance.Instance, error) {
	// Get all the container arguments
	var insts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		insts, err = tx.ContainerNodeProjectList("", instanceType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(insts, s)
}

// Load all instances of this nodes under the given project.
func instanceLoadNodeProjectAll(s *state.State, project string, instanceType instancetype.Type) ([]instance.Instance, error) {
	// Get all the container arguments
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.ContainerNodeProjectList(project, instanceType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

func instanceLoadAllInternal(dbInstances []db.Instance, s *state.State) ([]instance.Instance, error) {
	// Figure out what profiles are in use
	profiles := map[string]map[string]api.Profile{}
	for _, instArgs := range dbInstances {
		projectProfiles, ok := profiles[instArgs.Project]
		if !ok {
			projectProfiles = map[string]api.Profile{}
			profiles[instArgs.Project] = projectProfiles
		}
		for _, profile := range instArgs.Profiles {
			_, ok := projectProfiles[profile]
			if !ok {
				projectProfiles[profile] = api.Profile{}
			}
		}
	}

	// Get the profile data
	for project, projectProfiles := range profiles {
		for name := range projectProfiles {
			_, profile, err := s.Cluster.ProfileGet(project, name)
			if err != nil {
				return nil, err
			}

			projectProfiles[name] = *profile
		}
	}

	// Load the instances structs
	instances := []instance.Instance{}
	for _, dbInstance := range dbInstances {
		// Figure out the instances's profiles
		cProfiles := []api.Profile{}
		for _, name := range dbInstance.Profiles {
			cProfiles = append(cProfiles, profiles[dbInstance.Project][name])
		}

		args := db.ContainerToArgs(&dbInstance)
		inst, err := instanceLoad(s, args, cProfiles)
		if err != nil {
			return nil, err
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

// instanceLoad creates the underlying instance type struct and returns it as an Instance.
func instanceLoad(s *state.State, args db.InstanceArgs, profiles []api.Profile) (instance.Instance, error) {
	var inst instance.Instance
	var err error

	if args.Type == instancetype.Container {
		inst, err = containerLXCLoad(s, args, profiles)
	} else if args.Type == instancetype.VM {
		inst, err = vmqemu.Load(s, args, profiles)
	} else {
		return nil, fmt.Errorf("Invalid instance type for instance %s", args.Name)
	}

	if err != nil {
		return nil, err
	}

	return inst, nil
}

func autoCreateContainerSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Load all local instances
		allContainers, err := instanceLoadNodeAll(d.State(), instancetype.Any)
		if err != nil {
			logger.Error("Failed to load containers for scheduled snapshots", log.Ctx{"err": err})
			return
		}

		// Figure out which need snapshotting (if any)
		instances := []instance.Instance{}
		for _, c := range allContainers {
			schedule := c.ExpandedConfig()["snapshots.schedule"]

			if schedule == "" {
				continue
			}

			// Extend our schedule to one that is accepted by the used cron parser
			sched, err := cron.Parse(fmt.Sprintf("* %s", schedule))
			if err != nil {
				continue
			}

			// Check if it's time to snapshot
			now := time.Now()

			// Truncate the time now back to the start of the minute, before passing to
			// the cron scheduler, as it will add 1s to the scheduled time and we don't
			// want the next scheduled time to roll over to the next minute and break
			// the time comparison below.
			now = now.Truncate(time.Minute)

			// Calculate the next scheduled time based on the snapshots.schedule
			// pattern and the time now.
			next := sched.Next(now)

			// Ignore everything that is more precise than minutes.
			next = next.Truncate(time.Minute)

			if !now.Equal(next) {
				continue
			}

			// Check if the container is running
			if !shared.IsTrue(c.ExpandedConfig()["snapshots.schedule.stopped"]) && !c.IsRunning() {
				continue
			}

			instances = append(instances, c)
		}

		if len(instances) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return autoCreateContainerSnapshots(ctx, d, instances)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationSnapshotCreate, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start create snapshot operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Creating scheduled container snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to create scheduled container snapshots", log.Ctx{"err": err})
		}

		logger.Info("Done creating scheduled container snapshots")
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func autoCreateContainerSnapshots(ctx context.Context, d *Daemon, instances []instance.Instance) error {
	// Make the snapshots
	for _, c := range instances {
		ch := make(chan error)
		go func() {
			snapshotName, err := containerDetermineNextSnapshotName(d, c, "snap%d")
			if err != nil {
				logger.Error("Error retrieving next snapshot name", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			snapshotName = fmt.Sprintf("%s%s%s", c.Name(), shared.SnapshotDelimiter, snapshotName)

			expiry, err := shared.GetSnapshotExpiry(time.Now(), c.ExpandedConfig()["snapshots.expiry"])
			if err != nil {
				logger.Error("Error getting expiry date", log.Ctx{"err": err, "container": c})
				ch <- nil
				return
			}

			args := db.InstanceArgs{
				Architecture: c.Architecture(),
				Config:       c.LocalConfig(),
				Type:         c.Type(),
				Snapshot:     true,
				Devices:      c.LocalDevices(),
				Ephemeral:    c.IsEphemeral(),
				Name:         snapshotName,
				Profiles:     c.Profiles(),
				Project:      c.Project(),
				Stateful:     false,
				ExpiryDate:   expiry,
			}

			_, err = instanceCreateAsSnapshot(d.State(), args, c, nil)
			if err != nil {
				logger.Error("Error creating snapshots", log.Ctx{"err": err, "container": c})
			}

			ch <- nil
		}()
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

func pruneExpiredContainerSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Load all local instances
		allInstances, err := instanceLoadNodeAll(d.State(), instancetype.Any)
		if err != nil {
			logger.Error("Failed to load instances for snapshot expiry", log.Ctx{"err": err})
			return
		}

		// Figure out which need snapshotting (if any)
		expiredSnapshots := []instance.Instance{}
		for _, c := range allInstances {
			snapshots, err := c.Snapshots()
			if err != nil {
				logger.Error("Failed to list snapshots", log.Ctx{"err": err, "container": c.Name(), "project": c.Project()})
				continue
			}

			for _, snapshot := range snapshots {
				if snapshot.ExpiryDate().IsZero() {
					// Snapshot doesn't expire
					continue
				}

				if time.Now().Unix()-snapshot.ExpiryDate().Unix() >= 0 {
					expiredSnapshots = append(expiredSnapshots, snapshot)
				}
			}
		}

		if len(expiredSnapshots) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return pruneExpiredContainerSnapshots(ctx, d, expiredSnapshots)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationSnapshotsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired snapshots operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired instance snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to remove expired instance snapshots", log.Ctx{"err": err})
		}

		logger.Info("Done pruning expired instance snapshots")
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func pruneExpiredContainerSnapshots(ctx context.Context, d *Daemon, snapshots []instance.Instance) error {
	// Find snapshots to delete
	for _, snapshot := range snapshots {
		err := snapshot.Delete()
		if err != nil {
			return errors.Wrapf(err, "Failed to delete expired snapshot '%s' in project '%s'", snapshot.Name(), snapshot.Project())
		}
	}

	return nil
}

func containerDetermineNextSnapshotName(d *Daemon, c instance.Instance, defaultPattern string) (string, error) {
	var err error

	pattern := c.ExpandedConfig()["snapshots.pattern"]
	if pattern == "" {
		pattern = defaultPattern
	}

	pattern, err = shared.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		i := d.cluster.ContainerNextSnapshot(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	snapshots, err := c.Snapshots()
	if err != nil {
		return "", err
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	// Append '-0', '-1', etc. if the actual pattern/snapshot name already exists
	if snapshotExists {
		pattern = fmt.Sprintf("%s-%%d", pattern)
		i := d.cluster.ContainerNextSnapshot(c.Project(), c.Name(), pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}
