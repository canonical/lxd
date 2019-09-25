package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operation"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

type btrfsMigrationSourceDriver struct {
	container          instance.Instance
	snapshots          []instance.Instance
	btrfsSnapshotNames []string
	btrfs              *storageBtrfs
	runningSnapName    string
	stoppedSnapName    string
}

func (s *btrfsMigrationSourceDriver) send(conn *websocket.Conn, btrfsPath string, btrfsParent string, readWrapper func(io.ReadCloser) io.ReadCloser) error {
	args := []string{"send"}
	if btrfsParent != "" {
		args = append(args, "-p", btrfsParent)
	}
	args = append(args, btrfsPath)

	cmd := exec.Command("btrfs", args...)

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
		logger.Errorf("Problem reading btrfs send stderr: %s", err)
	}

	err = cmd.Wait()
	if err != nil {
		logger.Errorf("Problem with btrfs send: %s", string(output))
	}

	return err
}

func (s *btrfsMigrationSourceDriver) SendWhileRunning(conn *websocket.Conn, op *operation.Operation, bwlimit string, containerOnly bool) error {
	_, containerPool, _ := s.container.Storage().GetContainerPoolInfo()
	containerName := s.container.Name()
	containersPath := driver.GetContainerMountPoint("default", containerPool, "")
	sourceName := containerName

	// Deal with sending a snapshot to create a container on another LXD
	// instance.
	if s.container.IsSnapshot() {
		sourceName, _, _ := shared.ContainerGetParentAndSnapshotName(containerName)
		snapshotsPath := driver.GetSnapshotMountPoint(s.container.Project(), containerPool, sourceName)
		tmpContainerMntPoint, err := ioutil.TempDir(snapshotsPath, sourceName)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpContainerMntPoint)

		err = os.Chmod(tmpContainerMntPoint, 0700)
		if err != nil {
			return err
		}

		migrationSendSnapshot := fmt.Sprintf("%s/.migration-send", tmpContainerMntPoint)
		snapshotMntPoint := driver.GetSnapshotMountPoint(s.container.Project(), containerPool, containerName)
		err = s.btrfs.btrfsPoolVolumesSnapshot(snapshotMntPoint, migrationSendSnapshot, true, true)
		if err != nil {
			return err
		}
		defer btrfsSubVolumesDelete(migrationSendSnapshot)

		wrapper := StorageProgressReader(op, "fs_progress", containerName)
		return s.send(conn, migrationSendSnapshot, "", wrapper)
	}

	if !containerOnly {
		for i, snap := range s.snapshots {
			prev := ""
			if i > 0 {
				prev = driver.GetSnapshotMountPoint(snap.Project(), containerPool, s.snapshots[i-1].Name())
			}

			snapMntPoint := driver.GetSnapshotMountPoint(snap.Project(), containerPool, snap.Name())
			wrapper := StorageProgressReader(op, "fs_progress", snap.Name())
			if err := s.send(conn, snapMntPoint, prev, wrapper); err != nil {
				return err
			}
		}
	}

	tmpContainerMntPoint, err := ioutil.TempDir(containersPath, containerName)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpContainerMntPoint)

	err = os.Chmod(tmpContainerMntPoint, 0700)
	if err != nil {
		return err
	}

	migrationSendSnapshot := fmt.Sprintf("%s/.migration-send", tmpContainerMntPoint)
	containerMntPoint := driver.GetContainerMountPoint(s.container.Project(), containerPool, sourceName)
	err = s.btrfs.btrfsPoolVolumesSnapshot(containerMntPoint, migrationSendSnapshot, true, true)
	if err != nil {
		return err
	}
	defer btrfsSubVolumesDelete(migrationSendSnapshot)

	btrfsParent := ""
	if len(s.btrfsSnapshotNames) > 0 {
		btrfsParent = s.btrfsSnapshotNames[len(s.btrfsSnapshotNames)-1]
	}

	wrapper := StorageProgressReader(op, "fs_progress", containerName)
	return s.send(conn, migrationSendSnapshot, btrfsParent, wrapper)
}

func (s *btrfsMigrationSourceDriver) SendAfterCheckpoint(conn *websocket.Conn, bwlimit string) error {
	tmpPath := driver.GetSnapshotMountPoint(s.container.Project(), s.btrfs.pool.Name,
		fmt.Sprintf("%s/.migration-send", s.container.Name()))
	err := os.MkdirAll(tmpPath, 0711)
	if err != nil {
		return err
	}

	err = os.Chmod(tmpPath, 0700)
	if err != nil {
		return err
	}

	s.stoppedSnapName = fmt.Sprintf("%s/.root", tmpPath)
	parentName, _, _ := shared.ContainerGetParentAndSnapshotName(s.container.Name())
	containerMntPt := driver.GetContainerMountPoint(s.container.Project(), s.btrfs.pool.Name, parentName)
	err = s.btrfs.btrfsPoolVolumesSnapshot(containerMntPt, s.stoppedSnapName, true, true)
	if err != nil {
		return err
	}

	return s.send(conn, s.stoppedSnapName, s.runningSnapName, nil)
}

func (s *btrfsMigrationSourceDriver) Cleanup() {
	if s.stoppedSnapName != "" {
		btrfsSubVolumesDelete(s.stoppedSnapName)
	}

	if s.runningSnapName != "" {
		btrfsSubVolumesDelete(s.runningSnapName)
	}
}

func (s *btrfsMigrationSourceDriver) SendStorageVolume(conn *websocket.Conn, op *operation.Operation, bwlimit string, storage instance.Storage, volumeOnly bool) error {
	msg := fmt.Sprintf("Function not implemented")
	logger.Errorf(msg)
	return fmt.Errorf(msg)
}
