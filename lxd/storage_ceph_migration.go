package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	"github.com/pborman/uuid"
)

type rbdMigrationSourceDriver struct {
	container        container
	snapshots        []container
	rbdSnapshotNames []string
	ceph             *storageCeph
	runningSnapName  string
	stoppedSnapName  string
}

func (s *rbdMigrationSourceDriver) Snapshots() []container {
	return s.snapshots
}

func (s *rbdMigrationSourceDriver) Cleanup() {
	containerName := s.container.Name()

	if s.stoppedSnapName != "" {
		err := cephRBDSnapshotDelete(s.ceph.ClusterName, s.ceph.OSDPoolName,
			projectPrefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
			s.stoppedSnapName, s.ceph.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD snapshot "%s" of container "%s"`, s.stoppedSnapName, containerName)
		}
	}

	if s.runningSnapName != "" {
		err := cephRBDSnapshotDelete(s.ceph.ClusterName, s.ceph.OSDPoolName,
			projectPrefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
			s.runningSnapName, s.ceph.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD snapshot "%s" of container "%s"`, s.runningSnapName, containerName)
		}
	}
}

func (s *rbdMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	containerName := s.container.Name()
	s.stoppedSnapName = fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	err := cephRBDSnapshotCreate(s.ceph.ClusterName, s.ceph.OSDPoolName,
		projectPrefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
		s.stoppedSnapName, s.ceph.UserName)
	if err != nil {
		logger.Errorf(`Failed to create snapshot "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, s.stoppedSnapName, containerName, s.ceph.pool.Name, err)
		return err
	}

	cur := fmt.Sprintf("%s/container_%s@%s", s.ceph.OSDPoolName,
		projectPrefix(s.container.Project(), containerName), s.stoppedSnapName)
	err = s.rbdSend(conn, cur, s.runningSnapName, nil)
	if err != nil {
		logger.Errorf(`Failed to send exported diff of RBD storage volume "%s" from snapshot "%s": %s`, cur, s.runningSnapName, err)
		return err
	}
	logger.Debugf(`Sent exported diff of RBD storage volume "%s" from snapshot "%s"`, cur, s.stoppedSnapName)

	return nil
}

func (s *rbdMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn,
	op *operation, bwlimit string, containerOnly bool) error {
	containerName := s.container.Name()
	if s.container.IsSnapshot() {
		// ContainerSnapshotStart() will create the clone that is
		// referenced by sendName here.
		containerOnlyName, snapOnlyName, _ := containerGetParentAndSnapshotName(containerName)
		sendName := fmt.Sprintf(
			"%s/snapshots_%s_%s_start_clone",
			s.ceph.OSDPoolName,
			containerOnlyName,
			snapOnlyName)
		wrapper := StorageProgressReader(op, "fs_progress", containerName)

		err := s.rbdSend(conn, sendName, "", wrapper)
		if err != nil {
			logger.Errorf(`Failed to send RBD storage volume "%s": %s`, sendName, err)
			return err
		}
		logger.Debugf(`Sent RBD storage volume "%s"`, sendName)

		return nil
	}

	lastSnap := ""
	if !containerOnly {
		for i, snap := range s.rbdSnapshotNames {
			prev := ""
			if i > 0 {
				prev = s.rbdSnapshotNames[i-1]
			}

			lastSnap = snap

			sendSnapName := fmt.Sprintf(
				"%s/container_%s@%s",
				s.ceph.OSDPoolName,
				projectPrefix(s.container.Project(), containerName),
				snap)

			wrapper := StorageProgressReader(op, "fs_progress", snap)

			err := s.rbdSend(
				conn,
				sendSnapName,
				prev,
				wrapper)
			if err != nil {
				logger.Errorf(`Failed to send exported diff of RBD storage volume "%s" from snapshot "%s": %s`, sendSnapName, prev, err)
				return err
			}
			logger.Debugf(`Sent exported diff of RBD storage volume "%s" from snapshot "%s"`, sendSnapName, prev)
		}
	}

	s.runningSnapName = fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	err := cephRBDSnapshotCreate(s.ceph.ClusterName, s.ceph.OSDPoolName,
		projectPrefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
		s.runningSnapName, s.ceph.UserName)
	if err != nil {
		logger.Errorf(`Failed to create snapshot "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, s.runningSnapName, containerName, s.ceph.pool.Name, err)
		return err
	}

	cur := fmt.Sprintf("%s/container_%s@%s", s.ceph.OSDPoolName,
		projectPrefix(s.container.Project(), containerName), s.runningSnapName)
	wrapper := StorageProgressReader(op, "fs_progress", containerName)
	err = s.rbdSend(conn, cur, lastSnap, wrapper)
	if err != nil {
		logger.Errorf(`Failed to send exported diff of RBD storage volume "%s" from snapshot "%s": %s`, s.runningSnapName, lastSnap, err)
		return err
	}
	logger.Debugf(`Sent exported diff of RBD storage volume "%s" from snapshot "%s"`, s.runningSnapName, lastSnap)

	return nil
}

func (s *storageCeph) MigrationType() migration.MigrationFSType {
	return migration.MigrationFSType_RBD
}

func (s *storageCeph) PreservesInodes() bool {
	return false
}

func (s *storageCeph) MigrationSource(args MigrationSourceArgs) (MigrationStorageSourceDriver, error) {
	// If the container is a snapshot, let's just send that. We don't need
	// to send anything else, because that's all the user asked for.
	if args.Container.IsSnapshot() {
		return &rbdMigrationSourceDriver{
			container: args.Container,
			ceph:      s,
		}, nil
	}

	driver := rbdMigrationSourceDriver{
		container:        args.Container,
		snapshots:        []container{},
		rbdSnapshotNames: []string{},
		ceph:             s,
	}

	containerName := args.Container.Name()
	if args.ContainerOnly {
		logger.Debugf(`Only migrating the RBD storage volume for container "%s" on storage pool "%s`, containerName, s.pool.Name)
		return &driver, nil
	}

	// List all the snapshots in order of reverse creation. The idea here is
	// that we send the oldest to newest snapshot, hopefully saving on xfer
	// costs. Then, after all that, we send the container itself.
	snapshots, err := cephRBDVolumeListSnapshots(s.ClusterName,
		s.OSDPoolName, projectPrefix(args.Container.Project(), containerName),
		storagePoolVolumeTypeNameContainer, s.UserName)
	if err != nil {
		if err != db.ErrNoSuchObject {
			logger.Errorf(`Failed to list snapshots for RBD storage volume "%s" on storage pool "%s": %s`, containerName, s.pool.Name, err)
			return nil, err
		}
	}
	logger.Debugf(`Retrieved snapshots "%v" for RBD storage volume "%s" on storage pool "%s"`, snapshots, containerName, s.pool.Name)

	for _, snap := range snapshots {
		// In the case of e.g. multiple copies running at the same time,
		// we will have potentially multiple migration-send snapshots.
		// (Or in the case of the test suite, sometimes one will take
		// too long to delete.)
		if !strings.HasPrefix(snap, "snapshot_") {
			continue
		}

		lxdName := fmt.Sprintf("%s%s%s", containerName, shared.SnapshotDelimiter, snap[len("snapshot_"):])
		snapshot, err := containerLoadByProjectAndName(s.s, args.Container.Project(), lxdName)
		if err != nil {
			logger.Errorf(`Failed to load snapshot "%s" for RBD storage volume "%s" on storage pool "%s": %s`, lxdName, containerName, s.pool.Name, err)
			return nil, err
		}

		driver.snapshots = append(driver.snapshots, snapshot)
		driver.rbdSnapshotNames = append(driver.rbdSnapshotNames, snap)
	}

	return &driver, nil
}

func (s *storageCeph) MigrationSink(conn *websocket.Conn, op *operation, args MigrationSinkArgs) error {
	// Check that we received a valid root disk device with a pool property
	// set.
	parentStoragePool := ""
	parentExpandedDevices := args.Container.ExpandedDevices()
	parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := shared.GetRootDiskDevice(parentExpandedDevices)
	if parentLocalRootDiskDeviceKey != "" {
		parentStoragePool = parentLocalRootDiskDevice["pool"]
	}

	// A little neuroticism.
	if parentStoragePool == "" {
		return fmt.Errorf(`Detected that the container's root device ` +
			`is missing the pool property during RBD migration`)
	}
	logger.Debugf(`Detected root disk device with pool property set to "%s" during RBD migration`, parentStoragePool)

	// create empty volume for container
	// TODO: The cluster name can be different between LXD instances. Find
	// out what to do in this case. Maybe I'm overthinking this and if the
	// pool exists and we were able to initialize a new storage interface on
	// the receiving LXD instance it also means that s.ClusterName has been
	// set to the correct cluster name for that LXD instance. Yeah, I think
	// that's actually correct.
	containerName := args.Container.Name()
	if !cephRBDVolumeExists(s.ClusterName, s.OSDPoolName, projectPrefix(args.Container.Project(), containerName), storagePoolVolumeTypeNameContainer, s.UserName) {
		err := cephRBDVolumeCreate(s.ClusterName, s.OSDPoolName, projectPrefix(args.Container.Project(), containerName), storagePoolVolumeTypeNameContainer, "0", s.UserName)
		if err != nil {
			logger.Errorf(`Failed to create RBD storage volume "%s" for cluster "%s" in OSD pool "%s" on storage pool "%s": %s`, containerName, s.ClusterName, s.OSDPoolName, s.pool.Name, err)
			return err
		}
		logger.Debugf(`Created RBD storage volume "%s" on storage pool "%s"`, containerName, s.pool.Name)
	}

	if len(args.Snapshots) > 0 {
		snapshotMntPointSymlinkTarget := shared.VarPath("storage-pools", s.pool.Name, "containers-snapshots", projectPrefix(args.Container.Project(), containerName))
		snapshotMntPointSymlink := shared.VarPath("snapshots", projectPrefix(args.Container.Project(), containerName))
		if !shared.PathExists(snapshotMntPointSymlink) {
			err := os.Symlink(snapshotMntPointSymlinkTarget, snapshotMntPointSymlink)
			if err != nil {
				return err
			}
		}
	}

	// Now we're ready to receive the actual fs.
	recvName := fmt.Sprintf("%s/container_%s", s.OSDPoolName, projectPrefix(args.Container.Project(), containerName))
	for _, snap := range args.Snapshots {
		curSnapName := snap.GetName()
		ctArgs := snapshotProtobufToContainerArgs(args.Container.Project(), containerName, snap)

		// Ensure that snapshot and parent container have the same
		// storage pool in their local root disk device.  If the root
		// disk device for the snapshot comes from a profile on the new
		// instance as well we don't need to do anything.
		if ctArgs.Devices != nil {
			snapLocalRootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(ctArgs.Devices)
			if snapLocalRootDiskDeviceKey != "" {
				ctArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
			}
		}
		_, err := containerCreateEmptySnapshot(args.Container.DaemonState(), ctArgs)
		if err != nil {
			logger.Errorf(`Failed to create empty RBD storage volume for container "%s" on storage pool "%s: %s`, containerName, s.OSDPoolName, err)
			return err
		}
		logger.Debugf(`Created empty RBD storage volume for container "%s" on storage pool "%s`, containerName, s.OSDPoolName)

		wrapper := StorageProgressWriter(op, "fs_progress", curSnapName)
		err = s.rbdRecv(conn, recvName, wrapper)
		if err != nil {
			logger.Errorf(`Failed to receive RBD storage volume "%s": %s`, curSnapName, err)
			return err
		}
		logger.Debugf(`Received RBD storage volume "%s"`, curSnapName)

		snapshotMntPoint := getSnapshotMountPoint(args.Container.Project(), s.pool.Name, fmt.Sprintf("%s/%s", containerName, *snap.Name))
		if !shared.PathExists(snapshotMntPoint) {
			err := os.MkdirAll(snapshotMntPoint, 0700)
			if err != nil {
				return err
			}
		}
	}

	defer func() {
		snaps, err := cephRBDVolumeListSnapshots(s.ClusterName, s.OSDPoolName, projectPrefix(args.Container.Project(), containerName), storagePoolVolumeTypeNameContainer, s.UserName)
		if err == nil {
			for _, snap := range snaps {
				snapOnlyName, _, _ := containerGetParentAndSnapshotName(snap)
				if !strings.HasPrefix(snapOnlyName, "migration-send") {
					continue
				}

				err := cephRBDSnapshotDelete(s.ClusterName, s.OSDPoolName, projectPrefix(args.Container.Project(), containerName), storagePoolVolumeTypeNameContainer, snapOnlyName, s.UserName)
				if err != nil {
					logger.Warnf(`Failed to delete RBD container storage for snapshot "%s" of container "%s"`, snapOnlyName, containerName)
				}
			}
		}
	}()

	// receive the container itself
	wrapper := StorageProgressWriter(op, "fs_progress", containerName)
	err := s.rbdRecv(conn, recvName, wrapper)
	if err != nil {
		logger.Errorf(`Failed to receive RBD storage volume "%s": %s`, recvName, err)
		return err
	}
	logger.Debugf(`Received RBD storage volume "%s"`, recvName)

	if args.Live {
		err := s.rbdRecv(conn, recvName, wrapper)
		if err != nil {
			logger.Errorf(`Failed to receive RBD storage volume "%s": %s`, recvName, err)
			return err
		}
		logger.Debugf(`Received RBD storage volume "%s"`, recvName)
	}

	containerMntPoint := getContainerMountPoint(args.Container.Project(), s.pool.Name, containerName)
	err = createContainerMountpoint(
		containerMntPoint,
		args.Container.Path(),
		args.Container.IsPrivileged())
	if err != nil {
		logger.Errorf(`Failed to create mountpoint "%s" for RBD storage volume for container "%s" on storage pool "%s": %s"`, containerMntPoint, containerName, s.pool.Name, err)
		return err
	}
	logger.Debugf(`Created mountpoint "%s" for RBD storage volume for container "%s" on storage pool "%s""`, containerMntPoint, containerName, s.pool.Name)

	return nil
}
