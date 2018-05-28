package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/hashicorp/go-version"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
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
	auds := fmt.Sprintf("@lxd-p2c/%s", uuid.NewRandom().String())
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
		"-arvP",
		"--devices",
		"--numeric-ids",
		"--partial",
		"--sparse",
	}

	// extract rsync version
	result, _ := exec.Command("rsync", "--version").Output()
	re, _ := regexp.Compile(`.* ([0-9]+[.][0-9]+[.][0-9]+) .*`)
	extractVersion := re.FindStringSubmatch(string(result))
	rsyncVers, err := version.NewVersion(extractVersion[1])
	if err != nil {
		log.Fatal(err)
	}
	constraints, _ := version.NewConstraint(">= 3.1.0")
	if constraints.Check(rsyncVers) {
		args = append(args, []string{"--ignore-missing-args"}...)
	}

	if rsyncArgs != "" {
		args = append(args, strings.Split(rsyncArgs, " ")...)
	}

	args = append(args, []string{path, "localhost:/tmp/foo"}...)
	args = append(args, []string{"-e", rsyncCmd}...)

	cmd := exec.Command("rsync", args...)

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
