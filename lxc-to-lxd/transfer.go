package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/grant-he/lxd/lxd/migration"
	"github.com/grant-he/lxd/lxd/rsync"
	"github.com/grant-he/lxd/shared"
)

// Send an rsync stream of a path over a websocket
func rsyncSend(conn *websocket.Conn, path string, rsyncArgs string) error {
	cmd, dataSocket, stderr, err := rsyncSendSetup(path, rsyncArgs)
	if err != nil {
		return err
	}

	if dataSocket != nil {
		defer dataSocket.Close()
	}

	readDone, writeDone := shared.WebsocketMirror(conn, dataSocket, io.ReadCloser(dataSocket), nil, nil)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("Failed to rsync: %v\n%s", err, output)
	}

	err = cmd.Wait()
	<-readDone
	<-writeDone

	if err != nil {
		return fmt.Errorf("Failed to rsync: %v\n%s", err, output)
	}

	return nil
}

// Spawn the rsync process
func rsyncSendSetup(path string, rsyncArgs string) (*exec.Cmd, net.Conn, io.ReadCloser, error) {
	auds := fmt.Sprintf("@lxc-to-lxd/%s", uuid.NewRandom().String())
	if len(auds) > shared.ABSTRACT_UNIX_SOCK_LEN-1 {
		auds = auds[:shared.ABSTRACT_UNIX_SOCK_LEN-1]
	}

	l, err := net.Listen("unix", auds)
	if err != nil {
		return nil, nil, nil, err
	}

	execPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return nil, nil, nil, err
	}

	rsyncCmd := fmt.Sprintf("sh -c \"%s netcat %s\"", execPath, auds)

	args := []string{
		"-ar",
		"--devices",
		"--numeric-ids",
		"--partial",
		"--sparse",
		"--xattrs",
		"--delete",
		"--compress",
		"--compress-level=2",
	}

	if rsync.AtLeast("3.1.3") {
		args = append(args, "--filter=-x security.selinux")
	}

	if rsync.AtLeast("3.1.0") {
		args = append(args, "--ignore-missing-args")
	}

	if rsyncArgs != "" {
		args = append(args, strings.Split(rsyncArgs, " ")...)
	}

	args = append(args, []string{path, "localhost:/tmp/foo"}...)
	args = append(args, []string{"-e", rsyncCmd}...)

	cmd := exec.Command("rsync", args...)
	cmd.Stdout = os.Stderr

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}

	conn, err := l.Accept()
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, nil, nil, err
	}
	l.Close()

	return cmd, conn, stderr, nil
}

func protoSendError(ws *websocket.Conn, err error) {
	migration.ProtoSendControl(ws, err)

	if err != nil {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		ws.WriteMessage(websocket.CloseMessage, closeMsg)
		ws.Close()
	}
}
