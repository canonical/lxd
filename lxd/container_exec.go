package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "gopkg.in/inconshreveable/log15.v2"
)

type execWs struct {
	ttyWs

	command []string
	env     map[string]string
	rootUid int64
	rootGid int64

	ttys        []*os.File
	ptys        []*os.File
	controlExit chan bool
	doneCh      chan bool
	wgEOF       sync.WaitGroup
	cmdResult   int
}

func (s *execWs) Do(op *operation) error {
	<-s.allConnected

	stdin, stdout, stderr, err := s.openTTYs()
	if err != nil {
		return err
	}

	attachedChildIsBorn := make(chan int)

	if s.interactive {
		s.wgEOF.Add(1)
		go func() {
			attachedChildPid := <-attachedChildIsBorn
			select {
			case <-s.controlConnected:
				break

			case <-s.controlExit:
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
					logger.Debugf("Got error getting next reader %s", err)
					er, ok := err.(*websocket.CloseError)
					if !ok {
						break
					}

					if er.Code != websocket.CloseAbnormalClosure {
						break
					}

					// If an abnormal closure occurred, kill the attached process.
					err := syscall.Kill(attachedChildPid, syscall.SIGKILL)
					if err != nil {
						logger.Debugf("Failed to send SIGKILL to pid %d.", attachedChildPid)
					} else {
						logger.Debugf("Sent SIGKILL to pid %d.", attachedChildPid)
					}
					return
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					logger.Debugf("Failed to read message %s", err)
					break
				}

				command := api.ContainerTTYControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					logger.Debugf("Failed to unmarshal control socket command: %s", err)
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						logger.Debugf("Unable to extract window width: %s", err)
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						logger.Debugf("Unable to extract window height: %s", err)
						continue
					}

					err = shared.SetSize(int(s.ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						logger.Debugf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
						continue
					}
				} else if command.Command == "signal" {
					if err := syscall.Kill(attachedChildPid, syscall.Signal(command.Signal)); err != nil {
						logger.Debugf("Failed forwarding signal '%s' to PID %d.", command.Signal, attachedChildPid)
						continue
					}
					logger.Debugf("Forwarded signal '%d' to PID %d.", command.Signal, attachedChildPid)
				}
			}
		}()
		s.connectInteractiveStreams()
	} else {
		s.connectNotInteractiveStreams()
	}

	cmd, _, attachedPid, err := s.container.Exec(s.command, s.env, stdin, stdout, stderr, false)
	if err != nil {
		return err
	}

	if s.interactive {
		attachedChildIsBorn <- attachedPid
	}

	err = cmd.Wait()
	s.cmdResult = -1
	if err == nil {
		s.cmdResult = 0
	} else {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			if ok {
				s.cmdResult = status.ExitStatus()
			} else if status.Signaled() {
				// 128 + n == Fatal error signal "n"
				s.cmdResult = 128 + int(status.Signal())
			}
		}
	}
	s.finish(op)
	return op.UpdateMetadata(s.getMetadata())
}

// Open TTYs. Retruns stdin, stdout, stderr descriptors.
func (s *execWs) openTTYs() (*os.File, *os.File, *os.File, error) {
	var stdin *os.File
	var stdout *os.File
	var stderr *os.File
	var err error

	if s.interactive {
		s.ttys = make([]*os.File, 1)
		s.ptys = make([]*os.File, 1)
		s.ptys[0], s.ttys[0], err = shared.OpenPty(s.rootUid, s.rootGid)

		stdin = s.ttys[0]
		stdout = s.ttys[0]
		stderr = s.ttys[0]

		if s.width > 0 && s.height > 0 {
			shared.SetSize(int(s.ptys[0].Fd()), s.width, s.height)
		}
	} else {
		s.ttys = make([]*os.File, 3)
		s.ptys = make([]*os.File, 3)
		for i := 0; i < 3; i++ {
			s.ptys[i], s.ttys[i], err = shared.Pipe()
			if err != nil {
				return nil, nil, nil, err
			}
		}

		stdin = s.ptys[0]
		stdout = s.ttys[1]
		stderr = s.ttys[2]
	}

	return stdin, stdout, stderr, nil
}

func (s *execWs) connectInteractiveStreams() {
	go func() {
		s.connsLock.Lock()
		conn := s.conns[0]
		s.connsLock.Unlock()

		logger.Debugf("Starting to mirror websocket")
		readDone, writeDone := shared.WebsocketExecMirror(conn, s.ptys[0], s.ptys[0], s.doneCh, int(s.ptys[0].Fd()))

		<-readDone
		<-writeDone
		logger.Debugf("Finished to mirror websocket")

		conn.Close()
		s.wgEOF.Done()
	}()
}

func (s *execWs) connectNotInteractiveStreams() {
	s.wgEOF.Add(len(s.ttys) - 1)
	for i := 0; i < len(s.ttys); i++ {
		go func(i int) {
			if i == 0 {
				s.connsLock.Lock()
				conn := s.conns[0]
				s.connsLock.Unlock()

				<-shared.WebsocketRecvStream(s.ttys[0], conn)
				s.ttys[0].Close()
			} else {
				s.connsLock.Lock()
				conn := s.conns[i]
				s.connsLock.Unlock()

				<-shared.WebsocketSendStream(conn, s.ptys[i], -1)
				s.ptys[i].Close()
				s.wgEOF.Done()
			}
		}(i)
	}
}

func (s *execWs) getMetadata() shared.Jmap {
	return shared.Jmap{"return": s.cmdResult}
}

func (s *execWs) finish(op *operation) {
	for _, tty := range s.ttys {
		tty.Close()
	}

	s.connsLock.Lock()
	conn := s.conns[-1]
	s.connsLock.Unlock()

	if conn == nil {
		if s.interactive {
			s.controlExit <- true
		}
	} else {
		conn.Close()
	}

	s.doneCh <- true
	s.wgEOF.Wait()

	for _, pty := range s.ptys {
		pty.Close()
	}
}

func containerExecPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d.State(), name)
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
		ttyWs, err := newttyWs(c, post.Interactive, post.Width, post.Height)
		if err != nil {
			return InternalError(err)
		}
		ws := &execWs{
			ttyWs:       *ttyWs,
			command:     post.Command,
			env:         env,
			controlExit: make(chan bool),
			doneCh:      make(chan bool, 1),
		}
		idmapset, err := c.IdmapSet()
		if err != nil {
			return InternalError(err)
		}
		if idmapset != nil {
			ws.rootUid, ws.rootGid = idmapset.ShiftIntoNs(0, 0)
		}

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
			_, cmdResult, _, cmdErr = c.Exec(post.Command, env, nil, stdout, stderr, true)

			// Update metadata with the right URLs
			metadata["return"] = cmdResult
			metadata["output"] = shared.Jmap{
				"1": fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, c.Name(), filepath.Base(stdout.Name())),
				"2": fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, c.Name(), filepath.Base(stderr.Name())),
			}
		} else {
			_, cmdResult, _, cmdErr = c.Exec(post.Command, env, nil, nil, nil, true)
			metadata["return"] = cmdResult
		}

		err = op.UpdateMetadata(metadata)
		if err != nil {
			logger.Error("error updating metadata for cmd", log.Ctx{"err": err, "cmd": post.Command})
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
