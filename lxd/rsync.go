package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
)

func rsyncWebsocket(path string, cmd *exec.Cmd, conn *websocket.Conn) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	readDone, writeDone := shared.WebsocketMirror(conn, stdin, stdout)
	data, err2 := ioutil.ReadAll(stderr)
	if err2 != nil {
		shared.Debugf("error reading rsync stderr: %s", err2)
		return err2
	}

	err = cmd.Wait()
	if err != nil {
		shared.Debugf("rsync recv error for path %s: %s: %s", path, err, string(data))
	}

	<-readDone
	<-writeDone

	return err
}

func rsyncSendSetup(path string) (*exec.Cmd, net.Conn, io.ReadCloser, error) {
	/*
	 * It's sort of unfortunate, but there's no library call to get a
	 * temporary name, so we get the file and close it and use its name.
	 */
	f, err := ioutil.TempFile("", "lxd_rsync_")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Close()
	os.Remove(f.Name())

	/*
	 * The way rsync works, it invokes a subprocess that does the actual
	 * talking (given to it by a -E argument). Since there isn't an easy
	 * way for us to capture this process' stdin/stdout, we just use netcat
	 * and write to/from a unix socket.
	 *
	 * In principle we don't need this socket. It seems to me that some
	 * clever invocation of rsync --server --sender and usage of that
	 * process' stdin/stdout could work around the need for this socket,
	 * but I couldn't get it to work. Another option would be to look at
	 * the spawned process' first child and read/write from its
	 * stdin/stdout, but that also seemed messy. In any case, this seems to
	 * work just fine.
	 */
	l, err := net.Listen("unix", f.Name())
	if err != nil {
		return nil, nil, nil, err
	}

	/*
	 * Here, the path /tmp/foo is ignored. Since we specify localhost,
	 * rsync thinks we are syncing to a remote host (in this case, the
	 * other end of the lxd websocket), and so the path specified on the
	 * --server instance of rsync takes precedence.
	 *
	 * Additionally, we use sh -c instead of just calling nc directly
	 * because rsync passes a whole bunch of arguments to the wrapper
	 * command (i.e. the command to run on --server). However, we're
	 * hardcoding that at the other end, so we can just ignore it.
	 */
	rsyncCmd := fmt.Sprintf("sh -c \"nc -U %s\"", f.Name())
	cmd := exec.Command(
		"rsync",
		"-arvP",
		"--devices",
		"--numeric-ids",
		"--partial",
		path,
		"localhost:/tmp/foo",
		"-e",
		rsyncCmd)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}

	conn, err := l.Accept()
	if err != nil {
		return nil, nil, nil, err
	}
	l.Close()

	return cmd, conn, stderr, nil
}

// RsyncSend sets up the sending half of an rsync, to recursively send the
// directory pointed to by path over the websocket.
func RsyncSend(path string, conn *websocket.Conn) error {
	cmd, dataSocket, stderr, err := rsyncSendSetup(path)
	if dataSocket != nil {
		defer dataSocket.Close()
	}
	if err != nil {
		return err
	}

	readDone, writeDone := shared.WebsocketMirror(conn, dataSocket, dataSocket)

	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		shared.Debugf("problem reading rsync stderr %s", err)
	}

	if err := cmd.Wait(); err != nil {
		shared.Debugf("problem with rsync send of %s: %s: %s", path, err, string(output))
	}

	<-readDone
	<-writeDone

	return err
}

func rsyncRecvCmd(path string) *exec.Cmd {
	return exec.Command("rsync",
		"--server",
		"-vlogDtpre.iLsfx",
		"--numeric-ids",
		"--devices",
		"--partial",
		".",
		path)
}

// RsyncRecv sets up the receiving half of the websocket to rsync (the other
// half set up by RsyncSend), putting the contents in the directory specified
// by path.
func RsyncRecv(path string, conn *websocket.Conn) error {
	return rsyncWebsocket(path, rsyncRecvCmd(path), conn)
}
