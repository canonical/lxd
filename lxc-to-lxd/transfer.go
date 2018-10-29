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

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
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

	// Ignore deletions (requires 3.1 or higher)
	rsyncCheckVersion := func(min string) bool {
		out, err := shared.RunCommand("rsync", "--version")
		if err != nil {
			return false
		}

		fields := strings.Split(out, " ")
		curVer, err := version.Parse(fields[3])
		if err != nil {
			return false
		}

		minVer, err := version.Parse(min)
		if err != nil {
			return false
		}

		return curVer.Compare(minVer) >= 0
	}

	if rsyncCheckVersion("3.1.0") {
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
