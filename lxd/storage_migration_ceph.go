package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

type rbdMigrationSourceDriver struct {
	container        Instance
	snapshots        []Instance
	rbdSnapshotNames []string
	ceph             *storageCeph
	runningSnapName  string
	stoppedSnapName  string
}

func (s *rbdMigrationSourceDriver) Snapshots() []Instance {
	return s.snapshots
}

func (s *rbdMigrationSourceDriver) Cleanup() {
	containerName := s.container.Name()

	if s.stoppedSnapName != "" {
		err := cephRBDSnapshotDelete(s.ceph.ClusterName, s.ceph.OSDPoolName,
			project.Prefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
			s.stoppedSnapName, s.ceph.UserName)
		if err != nil {
			logger.Warnf(`Failed to delete RBD snapshot "%s" of container "%s"`, s.stoppedSnapName, containerName)
		}
	}

	if s.runningSnapName != "" {
		err := cephRBDSnapshotDelete(s.ceph.ClusterName, s.ceph.OSDPoolName,
			project.Prefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
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
		project.Prefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
		s.stoppedSnapName, s.ceph.UserName)
	if err != nil {
		logger.Errorf(`Failed to create snapshot "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, s.stoppedSnapName, containerName, s.ceph.pool.Name, err)
		return err
	}

	cur := fmt.Sprintf("%s/container_%s@%s", s.ceph.OSDPoolName,
		project.Prefix(s.container.Project(), containerName), s.stoppedSnapName)
	err = s.rbdSend(conn, cur, s.runningSnapName, nil)
	if err != nil {
		logger.Errorf(`Failed to send exported diff of RBD storage volume "%s" from snapshot "%s": %s`, cur, s.runningSnapName, err)
		return err
	}
	logger.Debugf(`Sent exported diff of RBD storage volume "%s" from snapshot "%s"`, cur, s.stoppedSnapName)

	return nil
}

func (s *rbdMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn,
	op *operations.Operation, bwlimit string, containerOnly bool) error {
	containerName := s.container.Name()
	if s.container.IsSnapshot() {
		// ContainerSnapshotStart() will create the clone that is
		// referenced by sendName here.
		containerOnlyName, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(containerName)
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
				project.Prefix(s.container.Project(), containerName),
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
		project.Prefix(s.container.Project(), containerName), storagePoolVolumeTypeNameContainer,
		s.runningSnapName, s.ceph.UserName)
	if err != nil {
		logger.Errorf(`Failed to create snapshot "%s" for RBD storage volume for image "%s" on storage pool "%s": %s`, s.runningSnapName, containerName, s.ceph.pool.Name, err)
		return err
	}

	cur := fmt.Sprintf("%s/container_%s@%s", s.ceph.OSDPoolName,
		project.Prefix(s.container.Project(), containerName), s.runningSnapName)
	wrapper := StorageProgressReader(op, "fs_progress", containerName)
	err = s.rbdSend(conn, cur, lastSnap, wrapper)
	if err != nil {
		logger.Errorf(`Failed to send exported diff of RBD storage volume "%s" from snapshot "%s": %s`, s.runningSnapName, lastSnap, err)
		return err
	}
	logger.Debugf(`Sent exported diff of RBD storage volume "%s" from snapshot "%s"`, s.runningSnapName, lastSnap)

	return nil
}

func (s *rbdMigrationSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operations.Operation, bwlimit string, storage storage, volumeOnly bool) error {
	msg := fmt.Sprintf("Function not implemented")
	logger.Errorf(msg)
	return fmt.Errorf(msg)
}

// Let's say we want to send the a container "a" including snapshots "snap0" and
// "snap1" on storage pool "pool1" from LXD "l1" to LXD "l2" on storage pool
// "pool2":
//
// The pool layout on "l1" would be:
//	pool1/container_a
//	pool1/container_a@snapshot_snap0
//	pool1/container_a@snapshot_snap1
//
// Then we need to send:
//	rbd export-diff pool1/container_a@snapshot_snap0 - | rbd import-diff - pool2/container_a
// (Note that pool2/container_a must have been created by the receiving LXD
// instance before.)
//	rbd export-diff pool1/container_a@snapshot_snap1 --from-snap snapshot_snap0 - | rbd import-diff - pool2/container_a
//	rbd export-diff pool1/container_a --from-snap snapshot_snap1 - | rbd import-diff - pool2/container_a
func (s *rbdMigrationSourceDriver) rbdSend(conn *websocket.Conn,
	volumeName string,
	volumeParentName string,
	readWrapper func(io.ReadCloser) io.ReadCloser) error {
	args := []string{
		"export-diff",
		"--cluster", s.ceph.ClusterName,
		volumeName,
	}

	if volumeParentName != "" {
		args = append(args, "--from-snap", volumeParentName)
	}

	// redirect output to stdout
	args = append(args, "-")

	cmd := exec.Command("rbd", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	readPipe := io.ReadCloser(stdout)
	if readWrapper != nil {
		readPipe = readWrapper(stdout)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	<-shared.WebsocketSendStream(conn, readPipe, 4*1024*1024)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Debugf(`Failed to read stderr output from "rbd export-diff": %s`, err)
	}

	err = cmd.Wait()
	if err != nil {
		logger.Errorf(`Failed to perform "rbd export-diff": %s`, string(output))
	}

	return err
}
