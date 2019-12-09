package main

import (
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rsync"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// MigrationStorageSourceDriver defines the functions needed to implement a
// migration source driver.
type MigrationStorageSourceDriver interface {
	/* send any bits of the container/snapshots that are possible while the
	 * container is still running.
	 */
	SendWhileRunning(conn *websocket.Conn, op *operations.Operation, bwlimit string, containerOnly bool) error

	/* send the final bits (e.g. a final delta snapshot for zfs, btrfs, or
	 * do a final rsync) of the fs after the container has been
	 * checkpointed. This will only be called when a container is actually
	 * being live migrated.
	 */
	SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error

	/* Called after either success or failure of a migration, can be used
	 * to clean up any temporary snapshots, etc.
	 */
	Cleanup()

	SendStorageVolume(conn *websocket.Conn, op *operations.Operation, bwlimit string, storage storage, volumeOnly bool) error
}

type rsyncStorageSourceDriver struct {
	container     instance.Instance
	snapshots     []instance.Instance
	rsyncFeatures []string
}

func (s rsyncStorageSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operations.Operation, bwlimit string, storage storage, volumeOnly bool) error {
	ourMount, err := storage.StoragePoolVolumeMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer storage.StoragePoolVolumeUmount()
	}

	state := storage.GetState()
	pool := storage.GetStoragePool()
	volume := storage.GetStoragePoolVolume()

	if !volumeOnly {
		snapshots, err := driver.VolumeSnapshotsGet(state, pool.Name, volume.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		for _, snap := range snapshots {
			wrapper := migration.ProgressTracker(op, "fs_progress", snap.Name)
			path := driver.GetStoragePoolVolumeSnapshotMountPoint(pool.Name, snap.Name)
			path = shared.AddSlash(path)
			logger.Debugf("Starting to send storage volume snapshot %s on storage pool %s from %s", snap.Name, pool.Name, path)

			err = rsync.Send(volume.Name, path, &shared.WebsocketIO{Conn: conn}, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
			if err != nil {
				return err
			}
		}
	}

	wrapper := migration.ProgressTracker(op, "fs_progress", volume.Name)
	path := driver.GetStoragePoolVolumeMountPoint(pool.Name, volume.Name)
	path = shared.AddSlash(path)
	logger.Debugf("Starting to send storage volume %s on storage pool %s from %s", volume.Name, pool.Name, path)
	err = rsync.Send(volume.Name, path, &shared.WebsocketIO{Conn: conn}, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
	if err != nil {
		return err
	}

	return nil
}

func (s rsyncStorageSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operations.Operation, bwlimit string, containerOnly bool) error {
	ctName, _, _ := shared.InstanceGetParentAndSnapshotName(s.container.Name())

	if !containerOnly {
		for _, send := range s.snapshots {
			ourStart, err := send.StorageStart()
			if err != nil {
				return err
			}
			if ourStart {
				defer send.StorageStop()
			}

			path := send.Path()
			wrapper := migration.ProgressTracker(op, "fs_progress", send.Name())
			state := s.container.DaemonState()
			err = rsync.Send(project.Prefix(s.container.Project(), ctName), shared.AddSlash(path), &shared.WebsocketIO{Conn: conn}, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
			if err != nil {
				return err
			}
		}
	}

	wrapper := migration.ProgressTracker(op, "fs_progress", s.container.Name())
	state := s.container.DaemonState()

	// Attempt to freeze the container to avoid changing files during transfer
	if s.container.IsRunning() {
		err := s.container.Freeze()
		if err != nil {
			logger.Errorf("Unable to freeze container during live-migration")
		} else {
			defer s.container.Unfreeze()
		}
	}

	return rsync.Send(project.Prefix(s.container.Project(), ctName), shared.AddSlash(s.container.Path()), &shared.WebsocketIO{Conn: conn}, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	ctName, _, _ := shared.InstanceGetParentAndSnapshotName(s.container.Name())
	// resync anything that changed between our first send and the checkpoint
	state := s.container.DaemonState()
	return rsync.Send(project.Prefix(s.container.Project(), ctName), shared.AddSlash(s.container.Path()), &shared.WebsocketIO{Conn: conn}, nil, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) Cleanup() {
	// noop
}

func rsyncStorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageSourceDriver{nil, nil, args.RsyncFeatures}, nil
}

func rsyncRefreshSource(refreshSnapshots []string, args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	var snapshots = []instance.Instance{}
	if !args.InstanceOnly {
		allSnapshots, err := args.Instance.Snapshots()
		if err != nil {
			return nil, err
		}

		for _, snap := range allSnapshots {
			_, snapName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name())
			if !shared.StringInSlice(snapName, refreshSnapshots) {
				continue
			}

			snapshots = append(snapshots, snap)
		}
	}

	return rsyncStorageSourceDriver{args.Instance, snapshots, args.RsyncFeatures}, nil
}

func rsyncMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	var err error
	var snapshots = []instance.Instance{}
	if !args.InstanceOnly {
		snapshots, err = args.Instance.Snapshots()
		if err != nil {
			return nil, err
		}
	}

	return rsyncStorageSourceDriver{args.Instance, snapshots, args.RsyncFeatures}, nil
}

func snapshotProtobufToInstanceArgs(project string, containerName string, snap *migration.Snapshot) db.InstanceArgs {
	config := map[string]string{}

	for _, ent := range snap.LocalConfig {
		config[ent.GetKey()] = ent.GetValue()
	}

	devices := deviceConfig.Devices{}
	for _, ent := range snap.LocalDevices {
		props := map[string]string{}
		for _, prop := range ent.Config {
			props[prop.GetKey()] = prop.GetValue()
		}

		devices[ent.GetName()] = props
	}

	name := containerName + shared.SnapshotDelimiter + snap.GetName()
	args := db.InstanceArgs{
		Architecture: int(snap.GetArchitecture()),
		Config:       config,
		Type:         instancetype.Container,
		Snapshot:     true,
		Devices:      devices,
		Ephemeral:    snap.GetEphemeral(),
		Name:         name,
		Profiles:     snap.Profiles,
		Stateful:     snap.GetStateful(),
		Project:      project,
	}

	if snap.GetCreationDate() != 0 {
		args.CreationDate = time.Unix(snap.GetCreationDate(), 0)
	}

	if snap.GetLastUsedDate() != 0 {
		args.LastUsedDate = time.Unix(snap.GetLastUsedDate(), 0)
	}

	return args
}

func rsyncStorageMigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
	err := args.Storage.StoragePoolVolumeCreate()
	if err != nil {
		return err
	}

	ourMount, err := args.Storage.StoragePoolVolumeMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer args.Storage.StoragePoolVolumeUmount()
	}

	pool := args.Storage.GetStoragePool()
	volume := args.Storage.GetStoragePoolVolume()

	if !args.VolumeOnly {
		for _, snap := range args.Snapshots {
			target := api.StorageVolumeSnapshotsPost{
				Name: fmt.Sprintf("%s/%s", volume.Name, *snap.Name),
			}

			dbArgs := &db.StorageVolumeArgs{
				Name:        fmt.Sprintf("%s/%s", volume.Name, *snap.Name),
				PoolName:    pool.Name,
				TypeName:    volume.Type,
				Snapshot:    true,
				Config:      volume.Config,
				Description: volume.Description,
			}

			_, err = storagePoolVolumeSnapshotDBCreateInternal(args.Storage.GetState(), dbArgs)
			if err != nil {
				return err
			}

			wrapper := migration.ProgressTracker(op, "fs_progress", target.Name)
			path := driver.GetStoragePoolVolumeMountPoint(pool.Name, volume.Name)
			path = shared.AddSlash(path)
			logger.Debugf("Starting to receive storage volume snapshot %s on storage pool %s into %s", target.Name, pool.Name, path)

			err = rsync.Recv(path, &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures)
			if err != nil {
				return err
			}

			err = args.Storage.StoragePoolVolumeSnapshotCreate(&target)
			if err != nil {
				return err
			}
		}
	}

	wrapper := migration.ProgressTracker(op, "fs_progress", volume.Name)
	path := driver.GetStoragePoolVolumeMountPoint(pool.Name, volume.Name)
	path = shared.AddSlash(path)
	logger.Debugf("Starting to receive storage volume %s on storage pool %s into %s", volume.Name, pool.Name, path)
	return rsync.Recv(path, &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures)
}

func rsyncMigrationSink(conn *websocket.Conn, op *operations.Operation, args MigrationSinkArgs) error {
	ourStart, err := args.Instance.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer args.Instance.StorageStop()
	}

	// At this point we have already figured out the parent container's root
	// disk device so we can simply retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := args.Instance.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices.CloneNative())
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf("the container's root device is missing the pool property")
	}

	localSnapshots, err := args.Instance.Snapshots()
	if err != nil {
		return err
	}

	if args.Instance.Type() != instancetype.Container {
		return fmt.Errorf("Instance type must be container")
	}

	ct := args.Instance.(*containerLXC)

	isDirBackend := ct.Storage().GetStorageType() == storageTypeDir
	if isDirBackend {
		if !args.InstanceOnly {
			for _, snap := range args.Snapshots {
				isSnapshotOutdated := true

				for _, localSnap := range localSnapshots {
					if localSnap.Name() == snap.GetName() {
						if localSnap.CreationDate().Unix() > snap.GetCreationDate() {
							isSnapshotOutdated = false
							break
						}
					}
				}

				// Only copy snapshot if it's outdated
				if !isSnapshotOutdated {
					continue
				}

				snapArgs := snapshotProtobufToInstanceArgs(args.Instance.Project(), args.Instance.Name(), snap)

				// Ensure that snapshot and parent container have the
				// same storage pool in their local root disk device.
				// If the root disk device for the snapshot comes from a
				// profile on the new instance as well we don't need to
				// do anything.
				if snapArgs.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapArgs.Devices.CloneNative())
					if snapLocalRootDiskDeviceKey != "" {
						snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				// Try and a load instance
				s, err := instance.LoadByProjectAndName(args.Instance.DaemonState(),
					args.Instance.Project(), snapArgs.Name)
				if err != nil {
					// Create the snapshot since it doesn't seem to exist
					s, err = containerCreateEmptySnapshot(args.Instance.DaemonState(), snapArgs)
					if err != nil {
						return err
					}
				}

				wrapper := migration.ProgressTracker(op, "fs_progress", s.Name())
				if err := rsync.Recv(shared.AddSlash(s.Path()), &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures); err != nil {
					return err
				}

				if args.Instance.Type() == instancetype.Container {
					c := args.Instance.(*containerLXC)
					err = resetContainerDiskIdmap(c, args.Idmap)
					if err != nil {
						return err
					}
				}
			}
		}

		wrapper := migration.ProgressTracker(op, "fs_progress", args.Instance.Name())
		err = rsync.Recv(shared.AddSlash(args.Instance.Path()), &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	} else {
		if !args.InstanceOnly {
			for _, snap := range args.Snapshots {
				isSnapshotOutdated := true

				for _, localSnap := range localSnapshots {
					if localSnap.Name() == snap.GetName() {
						if localSnap.CreationDate().Unix() > snap.GetCreationDate() {
							isSnapshotOutdated = false
							break
						}
					}
				}

				// Only copy snapshot if it's outdated
				if !isSnapshotOutdated {
					continue
				}

				snapArgs := snapshotProtobufToInstanceArgs(args.Instance.Project(), args.Instance.Name(), snap)

				// Ensure that snapshot and parent container have the
				// same storage pool in their local root disk device.
				// If the root disk device for the snapshot comes from a
				// profile on the new instance as well we don't need to
				// do anything.
				if snapArgs.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapArgs.Devices.CloneNative())
					if snapLocalRootDiskDeviceKey != "" {
						snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				wrapper := migration.ProgressTracker(op, "fs_progress", snap.GetName())
				err := rsync.Recv(shared.AddSlash(args.Instance.Path()), &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures)
				if err != nil {
					return err
				}

				if args.Instance.Type() == instancetype.Container {
					c := args.Instance.(*containerLXC)
					err = resetContainerDiskIdmap(c, args.Idmap)
					if err != nil {
						return err
					}
				}

				_, err = instance.LoadByProjectAndName(args.Instance.DaemonState(),
					args.Instance.Project(), snapArgs.Name)
				if err != nil {
					_, err = instanceCreateAsSnapshot(args.Instance.DaemonState(), snapArgs, args.Instance, op)
					if err != nil {
						return err
					}
				}
			}
		}

		wrapper := migration.ProgressTracker(op, "fs_progress", args.Instance.Name())
		err = rsync.Recv(shared.AddSlash(args.Instance.Path()), &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	}

	if args.Live {
		/* now receive the final sync */
		wrapper := migration.ProgressTracker(op, "fs_progress", args.Instance.Name())
		err := rsync.Recv(shared.AddSlash(args.Instance.Path()), &shared.WebsocketIO{Conn: conn}, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	}

	if args.Instance.Type() == instancetype.Container {
		c := args.Instance.(*containerLXC)
		err = resetContainerDiskIdmap(c, args.Idmap)
		if err != nil {
			return err
		}
	}

	return nil
}
