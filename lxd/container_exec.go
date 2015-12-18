package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

type commandPostContent struct {
	Command     []string          `json:"command"`
	WaitForWS   bool              `json:"wait-for-websocket"`
	Interactive bool              `json:"interactive"`
	Environment map[string]string `json:"environment"`
}

func runCommand(container *lxc.Container, command []string, options lxc.AttachOptions) (int, error) {
	status, err := container.RunCommandStatus(command, options)
	if err != nil {
		shared.Debugf("Failed running command: %q", err.Error())
		return 0, err
	}

	return status, nil
}

type execWs struct {
	command          []string
	container        *lxc.Container
	rootUid          int
	rootGid          int
	options          lxc.AttachOptions
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
	interactive      bool
	fds              map[int]string
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

			for i, c := range s.conns {
				if i != -1 && c == nil {
					return nil
				}
			}
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

	if s.interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)
		ptys[0], ttys[0], err = shared.OpenPty(s.rootUid, s.rootGid)
		s.options.StdinFd = ttys[0].Fd()
		s.options.StdoutFd = ttys[0].Fd()
		s.options.StderrFd = ttys[0].Fd()
	} else {
		ttys = make([]*os.File, 3)
		ptys = make([]*os.File, 3)
		for i := 0; i < len(ttys); i++ {
			ptys[i], ttys[i], err = shared.Pipe()
			if err != nil {
				return err
			}
		}
		s.options.StdinFd = ptys[0].Fd()
		s.options.StdoutFd = ttys[1].Fd()
		s.options.StderrFd = ttys[2].Fd()
	}

	controlExit := make(chan bool)
	var wgEOF sync.WaitGroup

	if s.interactive {
		wgEOF.Add(1)
		go func() {
			select {
			case <-s.controlConnected:
				break

			case <-controlExit:
				return
			}

			for {
				mt, r, err := s.conns[-1].NextReader()
				if mt == websocket.CloseMessage {
					break
				}

				if err != nil {
					shared.Debugf("Got error getting next reader %s", err)
					break
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					shared.Debugf("Failed to read message %s", err)
					break
				}

				command := shared.ContainerExecControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					shared.Debugf("Failed to unmarshal control socket command: %s", err)
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						shared.Debugf("Unable to extract window width: %s", err)
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						shared.Debugf("Unable to extract window height: %s", err)
						continue
					}

					err = shared.SetSize(int(ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						shared.Debugf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
						continue
					}
				}

				if err != nil {
					shared.Debugf("Got error writing to writer %s", err)
					break
				}
			}
		}()
		go func() {
			readDone, writeDone := shared.WebsocketMirror(s.conns[0], ptys[0], ptys[0])
			<-readDone
			<-writeDone
			s.conns[0].Close()
			wgEOF.Done()
		}()
	} else {
		wgEOF.Add(len(ttys) - 1)
		for i := 0; i < len(ttys); i++ {
			go func(i int) {
				if i == 0 {
					<-shared.WebsocketRecvStream(ttys[i], s.conns[i])
					ttys[i].Close()
				} else {
					<-shared.WebsocketSendStream(s.conns[i], ptys[i])
					ptys[i].Close()
					wgEOF.Done()
				}
			}(i)
		}
	}

	cmdResult, cmdErr := runCommand(
		s.container,
		s.command,
		s.options,
	)

	for _, tty := range ttys {
		tty.Close()
	}

	if s.conns[-1] == nil {
		if s.interactive {
			controlExit <- true
		}
	} else {
		s.conns[-1].Close()
	}

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

	post := commandPostContent{}
	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return BadRequest(err)
	}

	if err := json.Unmarshal(buf, &post); err != nil {
		return BadRequest(err)
	}

	opts := lxc.DefaultAttachOptions
	opts.ClearEnv = true
	opts.Env = []string{}

	for k, v := range c.ExpandedConfig() {
		if strings.HasPrefix(k, "environment.") {
			opts.Env = append(opts.Env, fmt.Sprintf("%s=%s", strings.TrimPrefix(k, "environment."), v))
		}
	}

	if post.Environment != nil {
		for k, v := range post.Environment {
			if k == "HOME" {
				opts.Cwd = v
			}
			opts.Env = append(opts.Env, fmt.Sprintf("%s=%s", k, v))
		}
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
		ws.options = opts
		for i := -1; i < len(ws.conns)-1; i++ {
			ws.fds[i], err = shared.RandomCryptoString()
			if err != nil {
				return InternalError(err)
			}
		}

		ws.command = post.Command
		ws.container = c.LXContainerGet()

		resources := map[string][]string{}
		resources["containers"] = []string{ws.container.Name()}

		op, err := operationCreate(operationClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	run := func(op *operation) error {
		nullDev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0666)
		if err != nil {
			return err
		}
		defer nullDev.Close()
		nullfd := nullDev.Fd()

		opts.StdinFd = nullfd
		opts.StdoutFd = nullfd
		opts.StderrFd = nullfd

		_, cmdErr := runCommand(c.LXContainerGet(), post.Command, opts)
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
