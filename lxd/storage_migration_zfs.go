package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

type zfsMigrationSourceDriver struct {
	instance         Instance
	snapshots        []Instance
	zfsSnapshotNames []string
	zfs              *storageZfs
	runningSnapName  string
	stoppedSnapName  string
	zfsFeatures      []string
}

func (s *zfsMigrationSourceDriver) send(conn *websocket.Conn, zfsName string, zfsParent string, readWrapper func(io.ReadCloser) io.ReadCloser) error {
	sourceParentName, _, _ := shared.ContainerGetParentAndSnapshotName(s.instance.Name())
	poolName := s.zfs.getOnDiskPoolName()
	args := []string{"send"}

	// Negotiated options
	if s.zfsFeatures != nil && len(s.zfsFeatures) > 0 {
		if shared.StringInSlice("compress", s.zfsFeatures) {
			args = append(args, "-c")
			args = append(args, "-L")
		}
	}

	args = append(args, []string{fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(s.instance.Project(), sourceParentName), zfsName)}...)
	if zfsParent != "" {
		args = append(args, "-i", fmt.Sprintf("%s/containers/%s@%s", poolName, project.Prefix(s.instance.Project(), s.instance.Name()), zfsParent))
	}

	cmd := exec.Command("zfs", args...)

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

	if err := cmd.Start(); err != nil {
		return err
	}

	<-shared.WebsocketSendStream(conn, readPipe, 4*1024*1024)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Errorf("Problem reading zfs send stderr: %s", err)
	}

	err = cmd.Wait()
	if err != nil {
		logger.Errorf("Problem with zfs send: %s", string(output))
	}

	return err
}

func (s *zfsMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operations.Operation, bwlimit string, containerOnly bool) error {
	if s.instance.IsSnapshot() {
		_, snapOnlyName, _ := shared.ContainerGetParentAndSnapshotName(s.instance.Name())
		snapshotName := fmt.Sprintf("snapshot-%s", snapOnlyName)
		wrapper := migration.ProgressReader(op, "fs_progress", s.instance.Name())
		return s.send(conn, snapshotName, "", wrapper)
	}

	lastSnap := ""
	if !containerOnly {
		for i, snap := range s.zfsSnapshotNames {
			prev := ""
			if i > 0 {
				prev = s.zfsSnapshotNames[i-1]
			}

			lastSnap = snap

			wrapper := migration.ProgressReader(op, "fs_progress", snap)
			if err := s.send(conn, snap, prev, wrapper); err != nil {
				return err
			}
		}
	}

	s.runningSnapName = fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	if err := zfsPoolVolumeSnapshotCreate(s.zfs.getOnDiskPoolName(), fmt.Sprintf("containers/%s", project.Prefix(s.instance.Project(), s.instance.Name())), s.runningSnapName); err != nil {
		return err
	}

	wrapper := migration.ProgressReader(op, "fs_progress", s.instance.Name())
	if err := s.send(conn, s.runningSnapName, lastSnap, wrapper); err != nil {
		return err
	}

	return nil
}

func (s *zfsMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	s.stoppedSnapName = fmt.Sprintf("migration-send-%s", uuid.NewRandom().String())
	if err := zfsPoolVolumeSnapshotCreate(s.zfs.getOnDiskPoolName(), fmt.Sprintf("containers/%s", project.Prefix(s.instance.Project(), s.instance.Name())), s.stoppedSnapName); err != nil {
		return err
	}

	if err := s.send(conn, s.stoppedSnapName, s.runningSnapName, nil); err != nil {
		return err
	}

	return nil
}

func (s *zfsMigrationSourceDriver) Cleanup() {
	poolName := s.zfs.getOnDiskPoolName()
	if s.stoppedSnapName != "" {
		zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(s.instance.Project(), s.instance.Name())), s.stoppedSnapName)
	}
	if s.runningSnapName != "" {
		zfsPoolVolumeSnapshotDestroy(poolName, fmt.Sprintf("containers/%s", project.Prefix(s.instance.Project(), s.instance.Name())), s.runningSnapName)
	}
}

func (s *zfsMigrationSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operations.Operation, bwlimit string, storage storage, volumeOnly bool) error {
	msg := fmt.Sprintf("Function not implemented")
	logger.Errorf(msg)
	return fmt.Errorf(msg)
}
