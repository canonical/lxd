package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "gopkg.in/inconshreveable/log15.v2"
)

type execWsOps struct {
	command []string
	env     map[string]string
	rootUid int64
	rootGid int64

	cmdResult           int
	attachedChildPid    int
	attachedChildIsBorn chan int
}

func (o *execWsOps) startup(s *ttyWs) {
	o.attachedChildPid = <-o.attachedChildIsBorn
}

func (o *execWsOps) do(s *ttyWs, stdin, stdout, stderr *os.File) error {
	cmd, _, attachedPid, err := s.container.Exec(o.command, o.env, stdin, stdout, stderr, false)
	if err != nil {
		return err
	}

	if s.interactive {
		o.attachedChildIsBorn <- attachedPid
	}

	err = cmd.Wait()
	o.cmdResult = -1
	if err == nil {
		o.cmdResult = 0
	} else {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			if ok {
				o.cmdResult = status.ExitStatus()
			} else if status.Signaled() {
				// 128 + n == Fatal error signal "n"
				o.cmdResult = 128 + int(status.Signal())
			}
		}
	}
	return nil
}

// Open TTYs. Retruns stdin, stdout, stderr descriptors.
func (o *execWsOps) openTTYs(s *ttyWs) (*os.File, *os.File, *os.File, error) {
	var stdin *os.File
	var stdout *os.File
	var stderr *os.File
	var err error

	if s.interactive {
		s.ttys = make([]*os.File, 1)
		s.ptys = make([]*os.File, 1)
		s.ptys[0], s.ttys[0], err = shared.OpenPty(o.rootUid, o.rootGid)

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

func (o *execWsOps) getMetadata(s *ttyWs) shared.Jmap {
	return shared.Jmap{"return": o.cmdResult}
}

func (o *execWsOps) handleSignal(s *ttyWs, signal int) {
	if o.attachedChildPid == 0 {
		return
	}
	if err := syscall.Kill(o.attachedChildPid, syscall.Signal(signal)); err != nil {
		logger.Debugf("Failed forwarding signal '%s' to PID %d.", signal, o.attachedChildPid)
		return
	}
	logger.Debugf("Forwarded signal '%d' to PID %d.", signal, o.attachedChildPid)
}

func (o *execWsOps) handleAbnormalClosure(s *ttyWs) {
	// If an abnormal closure occurred, kill the attached process.
	err := syscall.Kill(o.attachedChildPid, syscall.SIGKILL)
	if err != nil {
		logger.Debugf("Failed to send SIGKILL to pid %d.", o.attachedChildPid)
	} else {
		logger.Debugf("Sent SIGKILL to pid %d.", o.attachedChildPid)
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
		ops := &execWsOps{
			command:             post.Command,
			env:                 env,
			attachedChildIsBorn: make(chan int),
		}
		ws, err := newttyWs(ops, c, post.Interactive, post.Width, post.Height)
		if err != nil {
			return InternalError(err)
		}
		idmapset, err := c.IdmapSet()
		if err != nil {
			return InternalError(err)
		}
		if idmapset != nil {
			ops.rootUid, ops.rootGid = idmapset.ShiftIntoNs(0, 0)
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
