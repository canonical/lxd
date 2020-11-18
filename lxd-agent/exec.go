package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/operations"
	"github.com/grant-he/lxd/lxd/response"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
	"github.com/grant-he/lxd/shared/logger"
	"github.com/grant-he/lxd/shared/netutils"
)

var execCmd = APIEndpoint{
	Name: "exec",
	Path: "exec",

	Post: APIEndpointAction{Handler: execPost},
}

func execPost(d *Daemon, r *http.Request) response.Response {
	post := api.ContainerExecPost{}

	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	if err := json.Unmarshal(buf, &post); err != nil {
		return response.BadRequest(err)
	}

	env := map[string]string{}

	if post.Environment != nil {
		for k, v := range post.Environment {
			env[k] = v
		}
	}

	// Set default value for PATH
	_, ok := env["PATH"]
	if !ok {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	if shared.PathExists("/snap/bin") {
		env["PATH"] = fmt.Sprintf("%s:/snap/bin", env["PATH"])
	}

	// If running as root, set some env variables
	if post.User == 0 {
		// Set default value for HOME
		_, ok = env["HOME"]
		if !ok {
			env["HOME"] = "/root"
		}

		// Set default value for USER
		_, ok = env["USER"]
		if !ok {
			env["USER"] = "root"
		}
	}

	// Set default value for LANG
	_, ok = env["LANG"]
	if !ok {
		env["LANG"] = "C.UTF-8"
	}

	// Set the default working directory
	if post.Cwd == "" {
		post.Cwd = env["HOME"]
		if post.Cwd == "" {
			post.Cwd = "/"
		}
	}

	ws := &execWs{}
	ws.fds = map[int]string{}

	ws.conns = map[int]*websocket.Conn{}
	ws.conns[-1] = nil
	ws.conns[0] = nil
	if !post.Interactive {
		ws.conns[1] = nil
		ws.conns[2] = nil
	}
	ws.allConnected = make(chan bool, 1)
	ws.controlConnected = make(chan bool, 1)
	ws.interactive = post.Interactive
	for i := -1; i < len(ws.conns)-1; i++ {
		ws.fds[i], err = shared.RandomCryptoString()
		if err != nil {
			return response.InternalError(err)
		}
	}

	ws.command = post.Command
	ws.env = env

	ws.width = post.Width
	ws.height = post.Height

	ws.cwd = post.Cwd
	ws.uid = post.User
	ws.gid = post.Group

	resources := map[string][]string{}

	op, err := operations.OperationCreate(nil, "", operations.OperationClassWebsocket, db.OperationCommandExec, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
	if err != nil {
		return response.InternalError(err)
	}

	// Link the operation to the agent's event server.
	op.SetEventServer(d.events)

	return operations.OperationResponse(op)
}

type execWs struct {
	command          []string
	env              map[string]string
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
	interactive      bool
	fds              map[int]string
	width            int
	height           int
	uid              uint32
	gid              uint32
	cwd              string
}

func (s *execWs) Metadata() interface{} {
	fds := shared.Jmap{}
	for fd, secret := range s.fds {
		if fd == -1 {
			fds["control"] = secret
		} else {
			fds[strconv.Itoa(fd)] = secret
		}
	}

	return shared.Jmap{
		"fds":         fds,
		"command":     s.command,
		"environment": s.env,
		"interactive": s.interactive,
	}
}

func (s *execWs) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return fmt.Errorf("missing secret")
	}

	for fd, fdSecret := range s.fds {
		if secret == fdSecret {
			conn, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return err
			}

			s.connsLock.Lock()
			s.conns[fd] = conn
			s.connsLock.Unlock()

			if fd == -1 {
				s.controlConnected <- true
				return nil
			}

			s.connsLock.Lock()
			for i, c := range s.conns {
				if i != -1 && c == nil {
					s.connsLock.Unlock()
					return nil
				}
			}
			s.connsLock.Unlock()

			s.allConnected <- true
			return nil
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this Operation actually exists */
	return os.ErrPermission
}

func (s *execWs) Do(op *operations.Operation) error {
	<-s.allConnected

	var err error
	var ttys []*os.File
	var ptys []*os.File

	var stdin *os.File
	var stdout *os.File
	var stderr *os.File

	if s.interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)
		ptys[0], ttys[0], err = shared.OpenPty(int64(s.uid), int64(s.gid))
		if err != nil {
			return err
		}

		stdin = ttys[0]
		stdout = ttys[0]
		stderr = ttys[0]

		if s.width > 0 && s.height > 0 {
			shared.SetSize(int(ptys[0].Fd()), s.width, s.height)
		}
	} else {
		ttys = make([]*os.File, 3)
		ptys = make([]*os.File, 3)
		for i := 0; i < len(ttys); i++ {
			ptys[i], ttys[i], err = os.Pipe()
			if err != nil {
				return err
			}
		}

		stdin = ptys[0]
		stdout = ttys[1]
		stderr = ttys[2]
	}

	controlExit := make(chan bool, 1)
	attachedChildIsBorn := make(chan int)
	attachedChildIsDead := make(chan struct{})
	var wgEOF sync.WaitGroup

	if s.interactive {
		wgEOF.Add(1)
		go func() {
			logger.Debugf("Interactive child process handler waiting")
			defer logger.Debugf("Interactive child process handler finished")
			attachedChildPid := <-attachedChildIsBorn

			select {
			case <-s.controlConnected:
				break

			case <-controlExit:
				return
			}

			logger.Debugf("Interactive child process handler started for child PID %d", attachedChildPid)
			for {
				s.connsLock.Lock()
				conn := s.conns[-1]
				s.connsLock.Unlock()

				mt, r, err := conn.NextReader()
				if mt == websocket.CloseMessage {
					break
				}

				if err != nil {
					logger.Debugf("Got error getting next reader for child PID %d: %v", attachedChildPid, err)
					er, ok := err.(*websocket.CloseError)
					if !ok {
						break
					}

					if er.Code != websocket.CloseAbnormalClosure {
						break
					}

					// If an abnormal closure occurred, kill the attached process.
					err := unix.Kill(attachedChildPid, unix.SIGKILL)
					if err != nil {
						logger.Errorf("Failed to send SIGKILL to pid %d", attachedChildPid)
					} else {
						logger.Infof("Sent SIGKILL to pid %d", attachedChildPid)
					}
					return
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					logger.Errorf("Failed to read message %s", err)
					break
				}

				command := api.ContainerExecControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					logger.Errorf("Failed to unmarshal control socket command: %s", err)
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						logger.Errorf("Unable to extract window width: %s", err)
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						logger.Errorf("Unable to extract window height: %s", err)
						continue
					}

					err = shared.SetSize(int(ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						logger.Errorf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
						continue
					}
				} else if command.Command == "signal" {
					if err := unix.Kill(attachedChildPid, unix.Signal(command.Signal)); err != nil {
						logger.Errorf("Failed forwarding signal '%d' to PID %d", command.Signal, attachedChildPid)
						continue
					}
					logger.Infof("Forwarded signal '%d' to PID %d", command.Signal, attachedChildPid)
				}
			}
		}()

		go func() {
			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			logger.Info("Started mirroring websocket")
			readDone, writeDone := netutils.WebsocketExecMirror(conn, ptys[0], ptys[0], attachedChildIsDead, int(ptys[0].Fd()))

			<-readDone
			<-writeDone
			logger.Info("Finished mirroring websocket")

			conn.Close()
			wgEOF.Done()
		}()
	} else {
		wgEOF.Add(len(ttys) - 1)
		for i := 0; i < len(ttys); i++ {
			go func(i int) {
				if i == 0 {
					s.connsLock.Lock()
					conn := s.conns[i]
					s.connsLock.Unlock()

					<-shared.WebsocketRecvStream(ttys[i], conn)
					ttys[i].Close()
				} else {
					s.connsLock.Lock()
					conn := s.conns[i]
					s.connsLock.Unlock()

					<-shared.WebsocketSendStream(conn, ptys[i], -1)
					ptys[i].Close()
					wgEOF.Done()
				}
			}(i)
		}
	}

	finisher := func(cmdResult int, cmdErr error) error {
		for _, tty := range ttys {
			tty.Close()
		}

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()

		if conn == nil {
			if s.interactive {
				controlExit <- true
			}
		} else {
			conn.Close()
		}

		close(attachedChildIsDead)

		wgEOF.Wait()

		for _, pty := range ptys {
			pty.Close()
		}

		metadata := shared.Jmap{"return": cmdResult}
		err = op.UpdateMetadata(metadata)
		if err != nil {
			return err
		}

		return cmdErr
	}

	var cmd *exec.Cmd

	if len(s.command) > 1 {
		cmd = exec.Command(s.command[0], s.command[1:]...)
	} else {
		cmd = exec.Command(s.command[0])
	}

	// Prepare the environment
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: s.uid,
			Gid: s.gid,
		},
		// Creates a new session if the calling process is not a process group leader.
		// The calling process is the leader of the new session, the process group leader of
		// the new process group, and has no controlling terminal.
		// This is important to allow remote shells to handle ctrl+c.
		Setsid: true,
	}

	// Make the given terminal the controlling terminal of the calling process.
	// The calling process must be a session leader and not have a controlling terminal already.
	// This is important as allows ctrl+c to work as expected for non-shell programs.
	if s.interactive {
		cmd.SysProcAttr.Setctty = true
	}

	cmd.Dir = s.cwd

	err = cmd.Start()
	if err != nil {
		return finisher(-1, err)
	}

	if s.interactive {
		attachedChildIsBorn <- cmd.Process.Pid
	}

	err = cmd.Wait()
	if err == nil {
		return finisher(0, nil)
	}

	exitErr, ok := err.(*exec.ExitError)
	if ok {
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if ok {
			return finisher(status.ExitStatus(), nil)
		}

		if status.Signaled() {
			// 128 + n == Fatal error signal "n"
			return finisher(128+int(status.Signal()), nil)
		}
	}

	return finisher(-1, nil)
}
