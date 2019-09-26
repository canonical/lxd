package main

import (
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/project"
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
	SendWhileRunning(conn *websocket.Conn, op *operation, bwlimit string, containerOnly bool) error

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

	SendStorageVolume(conn *websocket.Conn, op *operation, bwlimit string, storage storage, volumeOnly bool) error
}

type rsyncStorageSourceDriver struct {
	container     Instance
	snapshots     []Instance
	rsyncFeatures []string
}

func (s rsyncStorageSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operation, bwlimit string, storage storage, volumeOnly bool) error {
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
		snapshots, err := storagePoolVolumeSnapshotsGet(state, pool.Name, volume.Name, storagePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		for _, snap := range snapshots {
			wrapper := StorageProgressReader(op, "fs_progress", snap)
			path := driver.GetStoragePoolVolumeSnapshotMountPoint(pool.Name, snap)
			path = shared.AddSlash(path)
			logger.Debugf("Starting to send storage volume snapshot %s on storage pool %s from %s", snap, pool.Name, path)

			err = RsyncSend(volume.Name, path, conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
			if err != nil {
				return err
			}
		}
	}

	wrapper := StorageProgressReader(op, "fs_progress", volume.Name)
	path := driver.GetStoragePoolVolumeMountPoint(pool.Name, volume.Name)
	path = shared.AddSlash(path)
	logger.Debugf("Starting to send storage volume %s on storage pool %s from %s", volume.Name, pool.Name, path)
	err = RsyncSend(volume.Name, path, conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
	if err != nil {
		return err
	}

	return nil
}

func (s rsyncStorageSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation, bwlimit string, containerOnly bool) error {
	ctName, _, _ := shared.ContainerGetParentAndSnapshotName(s.container.Name())

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
			wrapper := StorageProgressReader(op, "fs_progress", send.Name())
			state := s.container.DaemonState()
			err = RsyncSend(project.Prefix(s.container.Project(), ctName), shared.AddSlash(path), conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
			if err != nil {
				return err
			}
		}
	}

	wrapper := StorageProgressReader(op, "fs_progress", s.container.Name())
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

	return RsyncSend(project.Prefix(s.container.Project(), ctName), shared.AddSlash(s.container.Path()), conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	ctName, _, _ := shared.ContainerGetParentAndSnapshotName(s.container.Name())
	// resync anything that changed between our first send and the checkpoint
	state := s.container.DaemonState()
	return RsyncSend(project.Prefix(s.container.Project(), ctName), shared.AddSlash(s.container.Path()), conn, nil, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) Cleanup() {
	// noop
}

func rsyncStorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageSourceDriver{nil, nil, args.RsyncFeatures}, nil
}

func rsyncRefreshSource(refreshSnapshots []string, args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	var snapshots = []Instance{}
	if !args.InstanceOnly {
		allSnapshots, err := args.Instance.Snapshots()
		if err != nil {
			return nil, err
		}

		for _, snap := range allSnapshots {
			_, snapName, _ := shared.ContainerGetParentAndSnapshotName(snap.Name())
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
	var snapshots = []Instance{}
	if !args.InstanceOnly {
		snapshots, err = args.Instance.Snapshots()
		if err != nil {
			return nil, err
		}
	}

	return rsyncStorageSourceDriver{args.Instance, snapshots, args.RsyncFeatures}, nil
}

func snapshotProtobufToContainerArgs(project string, containerName string, snap *migration.Snapshot) db.ContainerArgs {
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
	args := db.ContainerArgs{
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

func rsyncStorageMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
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

			wrapper := StorageProgressWriter(op, "fs_progress", target.Name)
			path := driver.GetStoragePoolVolumeMountPoint(pool.Name, volume.Name)
			path = shared.AddSlash(path)
			logger.Debugf("Starting to receive storage volume snapshot %s on storage pool %s into %s", target.Name, pool.Name, path)

			err = RsyncRecv(path, conn, wrapper, args.RsyncFeatures)
			if err != nil {
				return err
			}

			err = args.Storage.StoragePoolVolumeSnapshotCreate(&target)
			if err != nil {
				return err
			}
		}
	}

	wrapper := StorageProgressWriter(op, "fs_progress", volume.Name)
	path := driver.GetStoragePoolVolumeMountPoint(pool.Name, volume.Name)
	path = shared.AddSlash(path)
	logger.Debugf("Starting to receive storage volume %s on storage pool %s into %s", volume.Name, pool.Name, path)
	return RsyncRecv(path, conn, wrapper, args.RsyncFeatures)
}

func rsyncMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
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

	isDirBackend := args.Instance.Storage().GetStorageType() == storageTypeDir
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

				snapArgs := snapshotProtobufToContainerArgs(args.Instance.Project(), args.Instance.Name(), snap)

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
				s, err := instanceLoadByProjectAndName(args.Instance.DaemonState(),
					args.Instance.Project(), snapArgs.Name)
				if err != nil {
					// Create the snapshot since it doesn't seem to exist
					s, err = containerCreateEmptySnapshot(args.Instance.DaemonState(), snapArgs)
					if err != nil {
						return err
					}
				}

				wrapper := StorageProgressWriter(op, "fs_progress", s.Name())
				if err := RsyncRecv(shared.AddSlash(s.Path()), conn, wrapper, args.RsyncFeatures); err != nil {
					return err
				}

				if args.Instance.Type() == instancetype.Container {
					c := args.Instance.(container)
					err = resetContainerDiskIdmap(c, args.Idmap)
					if err != nil {
						return err
					}
				}
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", args.Instance.Name())
		err = RsyncRecv(shared.AddSlash(args.Instance.Path()), conn, wrapper, args.RsyncFeatures)
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

				snapArgs := snapshotProtobufToContainerArgs(args.Instance.Project(), args.Instance.Name(), snap)

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

				wrapper := StorageProgressWriter(op, "fs_progress", snap.GetName())
				err := RsyncRecv(shared.AddSlash(args.Instance.Path()), conn, wrapper, args.RsyncFeatures)
				if err != nil {
					return err
				}

				if args.Instance.Type() == instancetype.Container {
					c := args.Instance.(container)
					err = resetContainerDiskIdmap(c, args.Idmap)
					if err != nil {
						return err
					}
				}

				_, err = instanceLoadByProjectAndName(args.Instance.DaemonState(),
					args.Instance.Project(), snapArgs.Name)
				if err != nil {
					_, err = containerCreateAsSnapshot(args.Instance.DaemonState(), snapArgs, args.Instance)
					if err != nil {
						return err
					}
				}
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", args.Instance.Name())
		err = RsyncRecv(shared.AddSlash(args.Instance.Path()), conn, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	}

	if args.Live {
		/* now receive the final sync */
		wrapper := StorageProgressWriter(op, "fs_progress", args.Instance.Name())
		err := RsyncRecv(shared.AddSlash(args.Instance.Path()), conn, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	}

	if args.Instance.Type() == instancetype.Container {
		c := args.Instance.(container)
		err = resetContainerDiskIdmap(c, args.Idmap)
		if err != nil {
			return err
		}
	}

	return nil
}
