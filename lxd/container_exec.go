package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"

	log "gopkg.in/inconshreveable/log15.v2"
)

type execWs struct {
	command   []string
	container container
	env       map[string]string

	rootUid          int
	rootGid          int
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
	interactive      bool
	fds              map[int]string
	width            int
	height           int
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

	return shared.Jmap{"fds": fds}
}

func (s *execWs) Connect(op *operation, r *http.Request, w http.ResponseWriter) error {
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
	 * which 403, not 404, since this operation actually exists */
	return os.ErrPermission
}

func (s *execWs) Do(op *operation) error {
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
		ptys[0], ttys[0], err = shared.OpenPty(s.rootUid, s.rootGid)

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
			ptys[i], ttys[i], err = shared.Pipe()
			if err != nil {
				return err
			}
		}

		stdin = ptys[0]
		stdout = ttys[1]
		stderr = ttys[2]
	}

	controlExit := make(chan bool)
	attachedChildIsBorn := make(chan int)
	attachedChildIsDead := make(chan bool, 1)
	var wgEOF sync.WaitGroup

	if s.interactive {
		wgEOF.Add(1)
		go func() {
			attachedChildPid := <-attachedChildIsBorn
			select {
			case <-s.controlConnected:
				break

			case <-controlExit:
				return
			}

			for {
				s.connsLock.Lock()
				conn := s.conns[-1]
				s.connsLock.Unlock()

				mt, r, err := conn.NextReader()
				if mt == websocket.CloseMessage {
					break
				}

				if err != nil {
					shared.LogDebugf("Got error getting next reader %s", err)
					break
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					shared.LogDebugf("Failed to read message %s", err)
					break
				}

				command := api.ContainerExecControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					shared.LogDebugf("Failed to unmarshal control socket command: %s", err)
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						shared.LogDebugf("Unable to extract window width: %s", err)
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						shared.LogDebugf("Unable to extract window height: %s", err)
						continue
					}

					err = shared.SetSize(int(ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						shared.LogDebugf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
						continue
					}
				} else if command.Command == "signal" {
					if err := syscall.Kill(attachedChildPid, syscall.Signal(command.Signal)); err != nil {
						shared.LogDebugf("Failed forwarding signal '%s' to PID %d.", command.Signal, attachedChildPid)
						continue
					}
					shared.LogDebugf("Forwarded signal '%d' to PID %d.", command.Signal, attachedChildPid)
				}
			}
		}()

		go func() {
			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			readDone, writeDone := shared.WebsocketExecMirror(conn, ptys[0], ptys[0], attachedChildIsDead, int(ptys[0].Fd()))

			<-readDone
			<-writeDone

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

		attachedChildIsDead <- true

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

	pid, attachedPid, err := s.container.Exec(s.command, s.env, stdin, stdout, stderr, false)
	if err != nil {
		return err
	}

	if s.interactive {
		attachedChildIsBorn <- attachedPid
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return finisher(-1, fmt.Errorf("Failed finding process: %q", err))
	}

	procState, err := proc.Wait()
	if err != nil {
		return finisher(-1, fmt.Errorf("Failed waiting on process %d: %q", pid, err))
	}

	if procState.Success() {
		return finisher(0, nil)
	}

	status, ok := procState.Sys().(syscall.WaitStatus)
	if ok {
		if status.Exited() {
			return finisher(status.ExitStatus(), nil)
		}

		if status.Signaled() {
			// 128 + n == Fatal error signal "n"
			return finisher(128+int(status.Signal()), nil)
		}
	}

	return finisher(-1, nil)
}

func containerExecPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return SmartError(err)
	}

	if !c.IsRunning() {
		return BadRequest(fmt.Errorf("Container is not running."))
	}

	if c.IsFrozen() {
		return BadRequest(fmt.Errorf("Container is frozen."))
	}

	post := api.ContainerExecPost{}
	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return BadRequest(err)
	}

	if err := json.Unmarshal(buf, &post); err != nil {
		return BadRequest(err)
	}

	env := map[string]string{}

	for k, v := range c.ExpandedConfig() {
		if strings.HasPrefix(k, "environment.") {
			env[strings.TrimPrefix(k, "environment.")] = v
		}
	}

	if post.Environment != nil {
		for k, v := range post.Environment {
			env[k] = v
		}
	}

	// Set default value for PATH
	_, ok := env["PATH"]
	if !ok {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		if c.FileExists("/snap") == nil {
			env["PATH"] = fmt.Sprintf("%s:/snap/bin", env["PATH"])
		}
	}

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

	// Set default value for USER
	_, ok = env["LANG"]
	if !ok {
		env["LANG"] = "C.UTF-8"
	}

	if post.WaitForWS {
		ws := &execWs{}
		ws.fds = map[int]string{}
		idmapset := c.IdmapSet()
		if idmapset != nil {
			ws.rootUid, ws.rootGid = idmapset.ShiftIntoNs(0, 0)
		}
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
				return InternalError(err)
			}
		}

		ws.command = post.Command
		ws.container = c
		ws.env = env

		ws.width = post.Width
		ws.height = post.Height

		resources := map[string][]string{}
		resources["containers"] = []string{ws.container.Name()}

		op, err := operationCreate(operationClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	run := func(op *operation) error {
		var cmdErr error
		var cmdResult int
		metadata := shared.Jmap{}

		if post.RecordOutput {
			// Prepare stdout and stderr recording
			stdout, err := os.OpenFile(filepath.Join(c.LogPath(), fmt.Sprintf("exec_%s.stdout", op.id)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
			if err != nil {
				return err
			}
			defer stdout.Close()

			stderr, err := os.OpenFile(filepath.Join(c.LogPath(), fmt.Sprintf("exec_%s.stderr", op.id)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
			if err != nil {
				return err
			}
			defer stderr.Close()

			// Run the command
			cmdResult, _, cmdErr = c.Exec(post.Command, env, nil, stdout, stderr, true)

			// Update metadata with the right URLs
			metadata["return"] = cmdResult
			metadata["output"] = shared.Jmap{
				"1": fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, c.Name(), filepath.Base(stdout.Name())),
				"2": fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, c.Name(), filepath.Base(stderr.Name())),
			}
		} else {
			cmdResult, _, cmdErr = c.Exec(post.Command, env, nil, nil, nil, true)
			metadata["return"] = cmdResult
		}

		err = op.UpdateMetadata(metadata)
		if err != nil {
			shared.LogError("error updating metadata for cmd", log.Ctx{"err": err, "cmd": post.Command})
		}

		return cmdErr
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
