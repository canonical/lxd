package main

import (
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// MigrationStorageSourceDriver defines the functions needed to implement a
// migration source driver.
type MigrationStorageSourceDriver interface {
	/* snapshots for this container, if any */
	Snapshots() []container

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

	SendStorageVolume(conn *websocket.Conn, op *operation, bwlimit string, storage storage) error
}

type rsyncStorageSourceDriver struct {
	container     container
	snapshots     []container
	rsyncFeatures []string
}

func (s rsyncStorageSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s rsyncStorageSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operation, bwlimit string, storage storage) error {
	ourMount, err := storage.StoragePoolVolumeMount()
	if err != nil {
		return err
	}
	if ourMount {
		defer storage.StoragePoolVolumeUmount()
	}

	pool := storage.GetStoragePool()
	volume := storage.GetStoragePoolVolume()

	wrapper := StorageProgressReader(op, "fs_progress", volume.Name)
	state := storage.GetState()
	path := getStoragePoolVolumeMountPoint(pool.Name, volume.Name)
	path = shared.AddSlash(path)
	logger.Debugf("Starting to send storage volume %s on storage pool %s from %s", volume.Name, pool.Name, path)
	return RsyncSend(volume.Name, path, conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation, bwlimit string, containerOnly bool) error {
	ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())

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
			err = RsyncSend(projectPrefix(s.container.Project(), ctName), shared.AddSlash(path), conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
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

	return RsyncSend(projectPrefix(s.container.Project(), ctName), shared.AddSlash(s.container.Path()), conn, wrapper, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())
	// resync anything that changed between our first send and the checkpoint
	state := s.container.DaemonState()
	return RsyncSend(projectPrefix(s.container.Project(), ctName), shared.AddSlash(s.container.Path()), conn, nil, s.rsyncFeatures, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) Cleanup() {
	// noop
}

func rsyncStorageMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	return rsyncStorageSourceDriver{nil, nil, args.RsyncFeatures}, nil
}

func rsyncRefreshSource(refreshSnapshots []string, args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	var snapshots = []container{}
	if !args.ContainerOnly {
		allSnapshots, err := args.Container.Snapshots()
		if err != nil {
			return nil, err
		}

		for _, snap := range allSnapshots {
			_, snapName, _ := containerGetParentAndSnapshotName(snap.Name())
			if !shared.StringInSlice(snapName, refreshSnapshots) {
				continue
			}

			snapshots = append(snapshots, snap)
		}
	}

	return rsyncStorageSourceDriver{args.Container, snapshots, args.RsyncFeatures}, nil
}

func rsyncMigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	var err error
	var snapshots = []container{}
	if !args.ContainerOnly {
		snapshots, err = args.Container.Snapshots()
		if err != nil {
			return nil, err
		}
	}

	return rsyncStorageSourceDriver{args.Container, snapshots, args.RsyncFeatures}, nil
}

func snapshotProtobufToContainerArgs(project string, containerName string, snap *migration.Snapshot) db.ContainerArgs {
	config := map[string]string{}

	for _, ent := range snap.LocalConfig {
		config[ent.GetKey()] = ent.GetValue()
	}

	devices := types.Devices{}
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
		Ctype:        db.CTypeSnapshot,
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

	wrapper := StorageProgressWriter(op, "fs_progress", volume.Name)
	path := getStoragePoolVolumeMountPoint(pool.Name, volume.Name)
	path = shared.AddSlash(path)
	logger.Debugf("Starting to receive storage volume %s on storage pool %s into %s", volume.Name, pool.Name, path)
	return RsyncRecv(path, conn, wrapper, args.RsyncFeatures)
}

func rsyncMigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	ourStart, err := args.Container.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer args.Container.StorageStop()
	}

	// At this point we have already figured out the parent container's root
	// disk device so we can simply retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := args.Container.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices)
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf("the container's root device is missing the pool property")
	}

	localSnapshots, err := args.Container.Snapshots()
	if err != nil {
		return err
	}

	isDirBackend := args.Container.Storage().GetStorageType() == storageTypeDir
	if isDirBackend {
		if !args.ContainerOnly {
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

				snapArgs := snapshotProtobufToContainerArgs(args.Container.Project(), args.Container.Name(), snap)

				// Ensure that snapshot and parent container have the
				// same storage pool in their local root disk device.
				// If the root disk device for the snapshot comes from a
				// profile on the new instance as well we don't need to
				// do anything.
				if snapArgs.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapArgs.Devices)
					if snapLocalRootDiskDeviceKey != "" {
						snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				// Try and a load container
				s, err := containerLoadByProjectAndName(args.Container.DaemonState(),
					args.Container.Project(), snapArgs.Name)
				if err != nil {
					// Create the snapshot since it doesn't seem to exist
					s, err = containerCreateEmptySnapshot(args.Container.DaemonState(), snapArgs)
					if err != nil {
						return err
					}
				}

				wrapper := StorageProgressWriter(op, "fs_progress", s.Name())
				if err := RsyncRecv(shared.AddSlash(s.Path()), conn, wrapper, args.RsyncFeatures); err != nil {
					return err
				}

				err = ShiftIfNecessary(args.Container, args.Idmap)
				if err != nil {
					return err
				}
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", args.Container.Name())
		err = RsyncRecv(shared.AddSlash(args.Container.Path()), conn, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	} else {
		if !args.ContainerOnly {
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

				snapArgs := snapshotProtobufToContainerArgs(args.Container.Project(), args.Container.Name(), snap)

				// Ensure that snapshot and parent container have the
				// same storage pool in their local root disk device.
				// If the root disk device for the snapshot comes from a
				// profile on the new instance as well we don't need to
				// do anything.
				if snapArgs.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(snapArgs.Devices)
					if snapLocalRootDiskDeviceKey != "" {
						snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				wrapper := StorageProgressWriter(op, "fs_progress", snap.GetName())
				err := RsyncRecv(shared.AddSlash(args.Container.Path()), conn, wrapper, args.RsyncFeatures)
				if err != nil {
					return err
				}

				err = ShiftIfNecessary(args.Container, args.Idmap)
				if err != nil {
					return err
				}

				_, err = containerLoadByProjectAndName(args.Container.DaemonState(),
					args.Container.Project(), snapArgs.Name)
				if err != nil {
					_, err = containerCreateAsSnapshot(args.Container.DaemonState(), snapArgs, args.Container)
					if err != nil {
						return err
					}
				}
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", args.Container.Name())
		err = RsyncRecv(shared.AddSlash(args.Container.Path()), conn, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	}

	if args.Live {
		/* now receive the final sync */
		wrapper := StorageProgressWriter(op, "fs_progress", args.Container.Name())
		err := RsyncRecv(shared.AddSlash(args.Container.Path()), conn, wrapper, args.RsyncFeatures)
		if err != nil {
			return err
		}
	}

	err = ShiftIfNecessary(args.Container, args.Idmap)
	if err != nil {
		return err
	}

	return nil
}
