package main

import (
	"io"
	"io/ioutil"
	"os/exec"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

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

func (s *storageCeph) rbdRecv(conn *websocket.Conn,
	volumeName string,
	writeWrapper func(io.WriteCloser) io.WriteCloser) error {
	args := []string{
		"import-diff",
		"--cluster", s.ClusterName,
		"-",
		volumeName,
	}

	cmd := exec.Command("rbd", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	writePipe := io.WriteCloser(stdin)
	if writeWrapper != nil {
		writePipe = writeWrapper(stdin)
	}

	<-shared.WebsocketRecvStream(writePipe, conn)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		logger.Debugf(`Failed to read stderr output from "rbd import-diff": %s`, err)
	}

	err = cmd.Wait()
	if err != nil {
		logger.Errorf(`Failed to perform "rbd import-diff": %s`, string(output))
	}

	return err
}
