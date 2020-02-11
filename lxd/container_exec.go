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

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/netutils"
	"github.com/lxc/lxd/shared/version"
)

type execWs struct {
	req api.InstanceExecPost

	instance         instance.Instance
	rootUid          int64
	rootGid          int64
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
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

	return shared.Jmap{
		"fds":         fds,
		"command":     s.req.Command,
		"environment": s.req.Environment,
		"interactive": s.req.Interactive,
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
	 * which 403, not 404, since this operation actually exists */
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

	if s.req.Interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)
		ptys[0], ttys[0], err = shared.OpenPty(s.rootUid, s.rootGid)
		if err != nil {
			return err
		}

		stdin = ttys[0]
		stdout = ttys[0]
		stderr = ttys[0]

		if s.req.Width > 0 && s.req.Height > 0 {
			shared.SetSize(int(ptys[0].Fd()), s.req.Width, s.req.Height)
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
	attachedChildIsBorn := make(chan instance.Cmd)
	attachedChildIsDead := make(chan bool, 1)
	var wgEOF sync.WaitGroup

	if s.req.Interactive {
		wgEOF.Add(1)
		go func() {
			logger.Debugf("Interactive child process handler waiting")
			defer logger.Debugf("Interactive child process handler finished")
			attachedChild := <-attachedChildIsBorn

			select {
			case <-s.controlConnected:
				break

			case <-controlExit:
				return
			}

			logger.Debugf(`Interactive child process handler started for child PID "%d"`, attachedChild.PID())
			for {
				s.connsLock.Lock()
				conn := s.conns[-1]
				s.connsLock.Unlock()

				mt, r, err := conn.NextReader()
				if mt == websocket.CloseMessage {
					break
				}

				if err != nil {
					logger.Debugf("Got error getting next reader: %v", err)
					er, ok := err.(*websocket.CloseError)
					if !ok {
						break
					}

					if er.Code != websocket.CloseAbnormalClosure {
						break
					}

					// If an abnormal closure occurred, kill the attached child.
					err := attachedChild.Signal(unix.SIGKILL)
					if err != nil {
						logger.Debugf(`Failed to send SIGKILL to PID "%d": %v`, attachedChild.PID(), err)
					} else {
						logger.Debugf(`Sent SIGKILL to PID "%d"`, attachedChild.PID())
					}
					return
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					logger.Debugf("Failed to read message: %v", err)
					break
				}

				command := api.InstanceExecControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					logger.Debugf("Failed to unmarshal control socket command: %v", err)
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						logger.Debugf("Unable to extract window width: %v", err)
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						logger.Debugf("Unable to extract window height: %v", err)
						continue
					}

					err = attachedChild.WindowResize(int(ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						logger.Debugf(`Failed to set window size to "%dx%d": %v`, winchWidth, winchHeight, err)
						continue
					}
				} else if command.Command == "signal" {
					err := attachedChild.Signal(unix.Signal(command.Signal))
					if err != nil {
						logger.Debugf(`Failed forwarding signal "%d" to PID "%d": %v`, command.Signal, attachedChild.PID(), err)
						continue
					}
				}
			}
		}()

		go func() {
			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			pollFD := 0

			// Use the polling mechanism for websocket mirror if local container, otherwise use reads.
			if s.instance.Type() == instancetype.Container {
				pollFD = int(ptys[0].Fd())
			}

			logger.Debugf("Started mirroring websocket with FD %v", pollFD)
			defer logger.Debugf("Finished mirroring websocket")
			readDone, writeDone := netutils.WebsocketExecMirror(conn, ptys[0], ptys[0], attachedChildIsDead, pollFD)
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

		// Ensure PTYs are closed before websocket to end wgEOF.Wait().
		for _, pty := range ptys {
			pty.Close()
		}

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()

		if conn == nil {
			if s.req.Interactive {
				controlExit <- true
			}
		} else {
			conn.Close()
		}

		attachedChildIsDead <- true
		wgEOF.Wait()

		metadata := shared.Jmap{"return": cmdResult}
		err = op.UpdateMetadata(metadata)
		if err != nil {
			return err
		}

		return cmdErr
	}

	cmd, err := s.instance.Exec(s.req, stdin, stdout, stderr)
	if err != nil {
		return err
	}

	if s.req.Interactive {
		// Start the interactive process handler.
		attachedChildIsBorn <- cmd
	}

	exitCode, err := cmd.Wait()
	if err != nil {
		return err
	}

	return finisher(exitCode, err)
}

func containerExecPost(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	post := api.InstanceExecPost{}
	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	if err := json.Unmarshal(buf, &post); err != nil {
		return response.BadRequest(err)
	}

	// Forward the request if the container is remote.
	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfContainerIsRemote(d.cluster, project, name, cert, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		url := fmt.Sprintf("/containers/%s/exec?project=%s", name, project)
		op, _, err := client.RawOperation("POST", url, post, "")
		if err != nil {
			return response.SmartError(err)
		}

		opAPI := op.Get()
		return operations.ForwardedOperationResponse(project, &opAPI)
	}

	inst, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Container is not running"))
	}

	if inst.IsFrozen() {
		return response.BadRequest(fmt.Errorf("Container is frozen"))
	}

	// Process environment.
	if post.Environment == nil {
		post.Environment = map[string]string{}
	}

	// Override any environment variable settings from the instance if not manually specified in post.
	for k, v := range inst.ExpandedConfig() {
		if strings.HasPrefix(k, "environment.") {
			envKey := strings.TrimPrefix(k, "environment.")
			if _, found := post.Environment[envKey]; !found {
				post.Environment[envKey] = v
			}
		}
	}

	// Set default value for PATH.
	_, ok := post.Environment["PATH"]
	if !ok {
		post.Environment["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		if inst.FileExists("/snap") == nil {
			post.Environment["PATH"] = fmt.Sprintf("%s:/snap/bin", post.Environment["PATH"])
		}
	}

	// If running as root, set some env variables.
	if post.User == 0 {
		// Set default value for HOME.
		_, ok = post.Environment["HOME"]
		if !ok {
			post.Environment["HOME"] = "/root"
		}

		// Set default value for USER.
		_, ok = post.Environment["USER"]
		if !ok {
			post.Environment["USER"] = "root"
		}
	}

	// Set default value for LANG.
	_, ok = post.Environment["LANG"]
	if !ok {
		post.Environment["LANG"] = "C.UTF-8"
	}

	if post.WaitForWS {
		ws := &execWs{}
		ws.fds = map[int]string{}

		if inst.Type() == instancetype.Container {
			c := inst.(*containerLXC)
			idmapset, err := c.CurrentIdmap()
			if err != nil {
				return response.InternalError(err)
			}

			if idmapset != nil {
				ws.rootUid, ws.rootGid = idmapset.ShiftIntoNs(0, 0)
			}
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
		for i := -1; i < len(ws.conns)-1; i++ {
			ws.fds[i], err = shared.RandomCryptoString()
			if err != nil {
				return response.InternalError(err)
			}
		}

		ws.instance = inst
		ws.req = post

		resources := map[string][]string{}
		resources["containers"] = []string{ws.instance.Name()}

		op, err := operations.OperationCreate(d.State(), project, operations.OperationClassWebsocket, db.OperationCommandExec, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	run := func(op *operations.Operation) error {
		metadata := shared.Jmap{}

		if post.RecordOutput {
			// Prepare stdout and stderr recording
			stdout, err := os.OpenFile(filepath.Join(inst.LogPath(), fmt.Sprintf("exec_%s.stdout", op.ID())), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
			if err != nil {
				return err
			}
			defer stdout.Close()

			stderr, err := os.OpenFile(filepath.Join(inst.LogPath(), fmt.Sprintf("exec_%s.stderr", op.ID())), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
			if err != nil {
				return err
			}
			defer stderr.Close()

			// Run the command
			cmd, err := inst.Exec(post, nil, stdout, stderr)
			if err != nil {
				return err
			}

			exitCode, err := cmd.Wait()
			if err != nil {
				return err
			}

			// Update metadata with the right URLs
			metadata["return"] = exitCode
			metadata["output"] = shared.Jmap{
				"1": fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, inst.Name(), filepath.Base(stdout.Name())),
				"2": fmt.Sprintf("/%s/containers/%s/logs/%s", version.APIVersion, inst.Name(), filepath.Base(stderr.Name())),
			}
		} else {
			cmd, err := inst.Exec(post, nil, nil, nil)
			if err != nil {
				return err
			}

			exitCode, err := cmd.Wait()
			if err != nil {
				return err
			}

			metadata["return"] = exitCode
		}

		err = op.UpdateMetadata(metadata)
		if err != nil {
			logger.Error("Error updating metadata for cmd", log.Ctx{"err": err, "cmd": post.Command})
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationCommandExec, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
