package main

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
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
}

type rsyncStorageSourceDriver struct {
	container container
	snapshots []container
}

func (s rsyncStorageSourceDriver) Snapshots() []container {
	return s.snapshots
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
			err = RsyncSend(ctName, shared.AddSlash(path), conn, wrapper, bwlimit, state.OS.ExecPath)
			if err != nil {
				return err
			}
		}
	}

	wrapper := StorageProgressReader(op, "fs_progress", s.container.Name())
	state := s.container.DaemonState()
	return RsyncSend(ctName, shared.AddSlash(s.container.Path()), conn, wrapper, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	ctName, _, _ := containerGetParentAndSnapshotName(s.container.Name())
	// resync anything that changed between our first send and the checkpoint
	state := s.container.DaemonState()
	return RsyncSend(ctName, shared.AddSlash(s.container.Path()), conn, nil, bwlimit, state.OS.ExecPath)
}

func (s rsyncStorageSourceDriver) Cleanup() {
	// noop
}

func rsyncMigrationSource(c container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	var err error
	var snapshots = []container{}
	if !containerOnly {
		snapshots, err = c.Snapshots()
		if err != nil {
			return nil, err
		}
	}

	return rsyncStorageSourceDriver{c, snapshots}, nil
}

func snapshotProtobufToContainerArgs(containerName string, snap *Snapshot) db.ContainerArgs {
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
	return db.ContainerArgs{
		Name:         name,
		Ctype:        db.CTypeSnapshot,
		Config:       config,
		Profiles:     snap.Profiles,
		Ephemeral:    snap.GetEphemeral(),
		Devices:      devices,
		Architecture: int(snap.GetArchitecture()),
		Stateful:     snap.GetStateful(),
	}
}

func rsyncMigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *idmap.IdmapSet, op *operation, containerOnly bool) error {
	ourStart, err := container.StorageStart()
	if err != nil {
		return err
	}
	if ourStart {
		defer container.StorageStop()
	}

	// At this point we have already figured out the parent container's root
	// disk device so we can simply retrieve it from the expanded devices.
	parentStoragePool := ""
	parentExpandedDevices := container.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := containerGetRootDiskDevice(parentExpandedDevices)
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf("the container's root device is missing the pool property")
	}

	isDirBackend := container.Storage().GetStorageType() == storageTypeDir
	if isDirBackend {
		if !containerOnly {
			for _, snap := range snapshots {
				args := snapshotProtobufToContainerArgs(container.Name(), snap)

				// Ensure that snapshot and parent container have the
				// same storage pool in their local root disk device.
				// If the root disk device for the snapshot comes from a
				// profile on the new instance as well we don't need to
				// do anything.
				if args.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := containerGetRootDiskDevice(args.Devices)
					if snapLocalRootDiskDeviceKey != "" {
						args.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				s, err := containerCreateEmptySnapshot(container.DaemonState(), args)
				if err != nil {
					return err
				}

				wrapper := StorageProgressWriter(op, "fs_progress", s.Name())
				if err := RsyncRecv(shared.AddSlash(s.Path()), conn, wrapper); err != nil {
					return err
				}

				err = ShiftIfNecessary(container, srcIdmap)
				if err != nil {
					return err
				}
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		err = RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper)
		if err != nil {
			return err
		}
	} else {
		if !containerOnly {
			for _, snap := range snapshots {
				args := snapshotProtobufToContainerArgs(container.Name(), snap)

				// Ensure that snapshot and parent container have the
				// same storage pool in their local root disk device.
				// If the root disk device for the snapshot comes from a
				// profile on the new instance as well we don't need to
				// do anything.
				if args.Devices != nil {
					snapLocalRootDiskDeviceKey, _, _ := containerGetRootDiskDevice(args.Devices)
					if snapLocalRootDiskDeviceKey != "" {
						args.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
					}
				}

				wrapper := StorageProgressWriter(op, "fs_progress", snap.GetName())
				err := RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper)
				if err != nil {
					return err
				}

				err = ShiftIfNecessary(container, srcIdmap)
				if err != nil {
					return err
				}

				_, err = containerCreateAsSnapshot(container.DaemonState(), args, container)
				if err != nil {
					return err
				}
			}
		}

		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		err = RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper)
		if err != nil {
			return err
		}
	}

	if live {
		/* now receive the final sync */
		wrapper := StorageProgressWriter(op, "fs_progress", container.Name())
		err := RsyncRecv(shared.AddSlash(container.Path()), conn, wrapper)
		if err != nil {
			return err
		}
	}

	err = ShiftIfNecessary(container, srcIdmap)
	if err != nil {
		return err
	}

	return nil
}
