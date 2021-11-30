package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/netutils"
)

const execWSControl = -1
const execWSStdin = 0
const execWSStdout = 1
const execWSStderr = 2

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
	ws.conns[execWSControl] = nil
	ws.conns[0] = nil // This is used for either TTY or Stdin.
	if !post.Interactive {
		ws.conns[execWSStdout] = nil
		ws.conns[execWSStderr] = nil
	}
	ws.requiredConnectedCtx, ws.requiredConnectedDone = context.WithCancel(context.Background())
	ws.interactive = post.Interactive

	for i := range ws.conns {
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

	op, err := operations.OperationCreate(nil, "", operations.OperationClassWebsocket, db.OperationCommandExec, resources, ws.Metadata(), ws.Do, nil, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	// Link the operation to the agent's event server.
	op.SetEventServer(d.events)

	return operations.OperationResponse(op)
}

type execWs struct {
	command               []string
	env                   map[string]string
	conns                 map[int]*websocket.Conn
	connsLock             sync.Mutex
	requiredConnectedCtx  context.Context
	requiredConnectedDone func()
	interactive           bool
	fds                   map[int]string
	width                 int
	height                int
	uid                   uint32
	gid                   uint32
	cwd                   string
}

func (s *execWs) Metadata() interface{} {
	fds := shared.Jmap{}
	for fd, secret := range s.fds {
		if fd == execWSControl {
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
			defer s.connsLock.Unlock()

			val, found := s.conns[fd]
			if found && val == nil {
				s.conns[fd] = conn

				for _, c := range s.conns {
					if c == nil {
						return nil // Not all required connections connected yet.
					}
				}

				s.requiredConnectedDone() // All required connections now connected.
				return nil
			} else if !found {
				return fmt.Errorf("Unknown websocket number")
			} else {
				return fmt.Errorf("Websocket number already connected")
			}
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this Operation actually exists */
	return os.ErrPermission
}

func (s *execWs) Do(op *operations.Operation) error {
	// Once this function ends ensure that any connected websockets are closed.
	defer func() {
		s.connsLock.Lock()
		for i := range s.conns {
			if s.conns[i] != nil {
				s.conns[i].Close()
			}
		}
		s.connsLock.Unlock()
	}()

	// As this function only gets called when the exec request has WaitForWS enabled, we expect the client to
	// connect to all of the required websockets within a short period of time and we won't proceed until then.
	logger.Debug("Waiting for exec websockets to connect")
	select {
	case <-s.requiredConnectedCtx.Done():
		break
	case <-time.After(time.Second * 5):
		return fmt.Errorf("Timed out waiting for websockets to connect")
	}

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

		stdin = ptys[execWSStdin]
		stdout = ttys[execWSStdout]
		stderr = ttys[execWSStderr]
	}

	controlExit := make(chan bool, 1)
	attachedChildIsDead := make(chan struct{})
	var wgEOF sync.WaitGroup

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
		exitCode := -1

		if errors.Is(err, exec.ErrNotFound) {
			exitCode = 127
			err = nil // Allow the exit code to be returned.
		}

		return finisher(exitCode, err)
	}

	logger := logging.AddContext(logger.Log, log.Ctx{"PID": cmd.Process.Pid, "interactive": s.interactive})
	logger.Debug("Instance process started")

	if s.interactive {
		wgEOF.Add(1)
		go func() {
			logger.Debug("Exec control handler started")
			defer logger.Debug("Exec control handler finished")

			for {
				s.connsLock.Lock()
				conn := s.conns[-1]
				s.connsLock.Unlock()

				mt, r, err := conn.NextReader()
				if mt == websocket.CloseMessage {
					break
				}

				if err != nil {
					logger.Debug("Got error getting next reader", log.Ctx{"err": err})
					er, ok := err.(*websocket.CloseError)
					if !ok {
						break
					}

					if er.Code != websocket.CloseAbnormalClosure {
						break
					}

					// If an abnormal closure occurred, kill the attached process.
					err := unix.Kill(cmd.Process.Pid, unix.SIGKILL)
					if err != nil {
						logger.Error("Failed to send SIGKILL")
					} else {
						logger.Info("Sent SIGKILL")
					}
					return
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					logger.Debug("Failed to read message", log.Ctx{"err": err})
					break
				}

				command := api.ContainerExecControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					logger.Debug("Failed to unmarshal control socket command", log.Ctx{"err": err})
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						logger.Debug("Unable to extract window width", log.Ctx{"err": err})
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						logger.Debug("Unable to extract window height", log.Ctx{"err": err})
						continue
					}

					err = shared.SetSize(int(ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						logger.Debug("Failed to set window size", log.Ctx{"err": err, "width": winchWidth, "height": winchHeight})
						continue
					}
				} else if command.Command == "signal" {
					if err := unix.Kill(cmd.Process.Pid, unix.Signal(command.Signal)); err != nil {
						logger.Debug("Failed forwarding signal", log.Ctx{"err": err, "signal": command.Signal})
						continue
					}
					logger.Info("Forwarded signal", log.Ctx{"signal": command.Signal})
				}
			}
		}()

		go func() {
			logger.Debug("Exec mirror websocket started", log.Ctx{"number": 0})
			defer logger.Debug("Exec mirror websocket finished", log.Ctx{"number": 0})

			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			readDone, writeDone := netutils.WebsocketExecMirror(conn, ptys[0], ptys[0], attachedChildIsDead, int(ptys[0].Fd()))

			<-readDone
			<-writeDone
			conn.Close()
			wgEOF.Done()
		}()
	} else {
		wgEOF.Add(len(ttys) - 1)
		for i := 0; i < len(ttys); i++ {
			go func(i int) {
				logger.Debug("Exec mirror websocket started", log.Ctx{"number": i})
				defer logger.Debug("Exec mirror websocket finished", log.Ctx{"number": i})

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

	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		exitCode = -1 // Unknown error.
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	logger.Debug("Instance process stopped", log.Ctx{"exitCode": exitCode})
	return finisher(exitCode, nil)
}
