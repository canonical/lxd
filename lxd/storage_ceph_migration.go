package main

import (
	"fmt"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
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
}

func (s *rbdMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	return nil
}

func (s *rbdMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation, bwlimit string, containerOnly bool) error {
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
				containerName,
				snap)

			wrapper := StorageProgressReader(op, "fs_progress", snap)

			err := s.rbdSend(
				conn,
				sendSnapName,
				prev,
				wrapper)
			if err != nil {
				logger.Errorf(`Failed to send exported diff `+
					`of RBD storage volume "%s" from `+
					`snapshot "%s": %s`, sendSnapName,
					prev, err)
				return err
			}
			logger.Debugf(`Sent exported diff of RBD storage `+
				`volume "%s" from snapshot "%s"`, sendSnapName,
				prev)
		}
	}

	sendName := fmt.Sprintf("%s/container_%s", s.ceph.OSDPoolName, containerName)
	wrapper := StorageProgressReader(op, "fs_progress", s.container.Name())

	err := s.rbdSend(conn, sendName, lastSnap, wrapper)
	if err != nil {
		logger.Errorf(`Failed to send exported diff of RBD storage `+
			`volume "%s" from snapshot "%s": %s`, sendName,
			lastSnap, err)
		return err
	}
	logger.Debugf(`Sent exported diff of RBD storage volume "%s" from `+
		`snapshot "%s"`, sendName, lastSnap)

	return nil
}

func (s *storageCeph) MigrationType() MigrationFSType {
	return MigrationFSType_RBD
}

func (s *storageCeph) PreservesInodes() bool {
	return false
}

func (s *storageCeph) MigrationSource(c container, containerOnly bool) (MigrationStorageSourceDriver, error) {
	// If the container is a snapshot, let's just send that. We don't need
	// to send anything else, because that's all the user asked for.
	if c.IsSnapshot() {
		return &rbdMigrationSourceDriver{
			container: c,
			ceph:      s,
		}, nil
	}

	driver := rbdMigrationSourceDriver{
		container:        c,
		snapshots:        []container{},
		rbdSnapshotNames: []string{},
		ceph:             s,
	}

	containerName := c.Name()
	if containerOnly {
		logger.Debugf(`Only migrating the RBD storage volume for `+
			`container "%s" on storage pool "%s`, containerName,
			s.pool.Name)
		return &driver, nil
	}

	// List all the snapshots in order of reverse creation. The idea here is
	// that we send the oldest to newest snapshot, hopefully saving on xfer
	// costs. Then, after all that, we send the container itself.
	snapshots, err := cephRBDVolumeListSnapshots(
		s.ClusterName,
		s.OSDPoolName,
		containerName,
		storagePoolVolumeTypeNameContainer)
	if err != nil {
		if err != NoSuchObjectError {
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
		snapshot, err := containerLoadByName(s.d, lxdName)
		if err != nil {
			logger.Errorf(`Failed to load snapshot "%s" for RBD storage volume "%s" on storage pool "%s": %s`, lxdName, containerName, s.pool.Name, err)
			return nil, err
		}

		driver.snapshots = append(driver.snapshots, snapshot)
		driver.rbdSnapshotNames = append(driver.rbdSnapshotNames, snap)
	}

	return &driver, nil
}

func (s *storageCeph) MigrationSink(live bool, container container, snapshots []*Snapshot, conn *websocket.Conn, srcIdmap *shared.IdmapSet, op *operation, containerOnly bool) error {
	return nil
}
