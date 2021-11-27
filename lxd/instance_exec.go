package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/netutils"
	"github.com/lxc/lxd/shared/version"
)

const execWSControl = -1
const execWSStdin = 0
const execWSStdout = 1
const execWSSterr = 2

type execWs struct {
	req api.InstanceExecPost

	instance               instance.Instance
	conns                  map[int]*websocket.Conn
	connsLock              sync.Mutex
	wsNeededConnected      context.Context
	wsNeededConnectedDone  func()
	wsControlConnected     context.Context
	wsControlConnectedDone func()
	fds                    map[int]string
	s                      *state.State
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
			defer s.connsLock.Unlock()

			val, found := s.conns[fd]

			if found && val == nil {
				s.conns[fd] = conn

				if fd == execWSControl {
					s.wsControlConnectedDone() // Control connection connected.
				}

				for i, c := range s.conns {
					if i == execWSControl && s.req.WaitForWS && !s.req.Interactive {
						// Due to a historical bug in the LXC CLI command, we cannot force
						// the client to connect a control socket when in non-interactive
						// mode. This is because the older CLI tools did not connect this
						// channel and so we would prevent the older CLIs connecitng to
						// newer servers. So skip the control connection from being
						// considered as a needed connection in this case.
						continue
					}

					if c == nil {
						return nil // Not all needed connections connected yet.
					}
				}

				s.wsNeededConnectedDone() // All needed connections now connected.
				return nil
			} else if !found {
				return fmt.Errorf("Unknown websocket number")
			} else {
				return fmt.Errorf("Websocket number already connected")
			}
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this operation actually exists */
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
	// connect to all of the expected websockets within a short period of time and we won't proceed until then.
	logger.Debug("Waiting for exec websockets to connect")
	select {
	case <-s.wsNeededConnected.Done():
		break
	case <-time.After(time.Second * 5):
		return fmt.Errorf("Timed out waiting for websockets to connect")
	}

	revert := revert.New()
	defer revert.Fail()

	var err error
	var ttys []*os.File
	var ptys []*os.File

	var stdin *os.File
	var stdout *os.File
	var stderr *os.File

	if s.req.Interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)

		var devptsFd *os.File
		var rootUID, rootGID int64

		if s.instance.Type() == instancetype.Container {
			c := s.instance.(instance.Container)
			idmapset, err := c.CurrentIdmap()
			if err != nil {
				return err
			}

			if idmapset != nil {
				rootUID, rootGID = idmapset.ShiftIntoNs(0, 0)
			}

			devptsFd, _ = c.DevptsFd()
		}

		if devptsFd != nil {
			if s.s.OS.NativeTerminals {
				ptys[0], ttys[0], err = shared.OpenPtyInDevpts(int(devptsFd.Fd()), rootUID, rootGID)
			}

			devptsFd.Close()
			devptsFd = nil
		} else {
			ptys[0], ttys[0], err = shared.OpenPty(rootUID, rootGID)
		}
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
			ptys[i], ttys[i], err = os.Pipe()
			if err != nil {
				return err
			}
		}

		stdin = ptys[execWSStdin]
		stdout = ttys[execWSStdout]
		stderr = ttys[execWSSterr]
	}

	// Ensure any configured TTY and PTY handles are closed on error.
	revert.Add(func() {
		for _, tty := range ttys {
			tty.Close()
		}

		for _, pty := range ptys {
			pty.Close()
		}
	})

	// Create channel for indicating once the command has finished, and ensure its closed on error.
	attachedChildCtx, attachedChildDone := context.WithCancel(context.Background())
	defer attachedChildDone()

	// Create wait group to indicate when the websocket go routines have finished.
	var wgEOF sync.WaitGroup

	// Run the command in the instance.
	cmd, err := s.instance.Exec(s.req, stdin, stdout, stderr)
	if err != nil {
		return err
	}

	cmdKill := func() {
		err := cmd.Signal(unix.SIGKILL)
		if err != nil {
			logger.Debug("Failed to send SIGKILL signal", log.Ctx{"err": err})
		} else {
			logger.Debug("Sent SIGKILL signal")
		}
	}

	var cmdKillOnce sync.Once

	logger := logging.AddContext(logger.Log, log.Ctx{"instance": s.instance.Name(), "PID": cmd.PID(), "interactive": s.req.Interactive})
	logger.Debug("Instance process started")

	wgEOF.Add(1)
	go func() {
		defer wgEOF.Done()

		<-s.wsControlConnected.Done()
		s.connsLock.Lock()
		conn := s.conns[execWSControl]
		s.connsLock.Unlock()

		if conn == nil {
			return // No control connection, just being asked to finish go routine.
		}

		defer conn.Close()

		logger.Debug("Control handler started")
		defer logger.Debug("Control handler finished")

		for {
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

				// If an abnormal closure occurred, kill the attached child.
				cmdKillOnce.Do(cmdKill)

				return
			}

			buf, err := ioutil.ReadAll(r)
			if err != nil {
				logger.Debug("Failed to read message", log.Ctx{"err": err})
				break
			}

			command := api.InstanceExecControl{}

			if err := json.Unmarshal(buf, &command); err != nil {
				logger.Debug("Failed to unmarshal control socket command", log.Ctx{"err": err})
				continue
			}

			if command.Command == "window-resize" && s.req.Interactive {
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

				err = cmd.WindowResize(int(ptys[0].Fd()), winchWidth, winchHeight)
				if err != nil {
					logger.Debug("Failed to set window size", log.Ctx{"err": err, "width": winchWidth, "height": winchHeight})
					continue
				}
			} else if command.Command == "signal" {
				err := cmd.Signal(unix.Signal(command.Signal))
				if err != nil {
					logger.Debug("Failed forwarding signal", log.Ctx{"err": err, "signal": command.Signal})
					continue
				}
			}
		}
	}()

	// Now that process has started, we can start the mirroring of the process channels and websockets.
	if s.req.Interactive {
		wgEOF.Add(1)
		go func() {
			defer wgEOF.Done()

			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			logger.Debug("Started mirroring websocket", "fd", 0)
			defer logger.Debug("Finished mirroring websocket", "fd", 0)

			readDone, writeDone := netutils.WebsocketExecMirror(attachedChildCtx, conn, ptys[0], ptys[0], int(ptys[0].Fd()))

			<-readDone
			<-writeDone
			conn.Close()
		}()
	} else {
		wgEOF.Add(len(ttys))
		for i := 0; i < len(ttys); i++ {
			go func(i int) {
				defer wgEOF.Done()

				logger.Debug("Started mirroring websocket", "fd", i)
				defer logger.Debug("Finished mirroring websocket", "fd", i)

				s.connsLock.Lock()
				conn := s.conns[i]
				s.connsLock.Unlock()

				if i == execWSStdin {
					<-shared.WebsocketRecvStream(ttys[i], conn)
					ttys[i].Close()
					conn.Close()
				} else {
					if i == execWSStdout {
						// Launch a go routine that reads from stdout. This will be used to
						// detect when the client disconnects, as normally there should be
						// no data received on the stdout channel from the client. This is
						// needed in cases where the control connection isn't used by the
						// client and so we need to detect when the client disconnects to
						// avoid leaving the command running in the background.
						go func() {
							_, _, err := conn.ReadMessage()

							// If the control has connected, then leave it to that
							// handler to clean the command up. The control context
							// gets cancelled when the command exists, so this will
							// also be used to indicate if the command exited normally.
							if s.wsControlConnected.Err() == nil {
								logger.Debug("Unexpected read on websocket, killing command", "fd", i, "err", err)
								cmdKillOnce.Do(cmdKill)
							}
						}()
					}

					<-shared.WebsocketSendStream(conn, ptys[i], -1)
					ptys[i].Close()
					conn.Close()
				}
			}(i)
		}
	}

	exitCode, err := cmd.Wait()
	logger.Debug("Instance process stopped")

	// Close any of the active TTY handles now command has finished.
	for _, tty := range ttys {
		tty.Close()
	}

	// Ensure that the control handler wakes up to finish (if asleep due to no earlier control connection).
	// Also used to indicate that the stdout reader doesn't need to take charge of killing the command.
	s.wsControlConnectedDone()

	// If there is a control connection, close it, to ensure the control handler finishes.
	s.connsLock.Lock()
	controlConn := s.conns[execWSControl]
	if controlConn != nil {
		controlConn.Close()
	}
	s.connsLock.Unlock()

	// Indicate to the go routines above that the command has finished.
	attachedChildDone()

	wgEOF.Wait() // Wait for go routines to finish.

	// Now the go routines have finished, close the PTY handles.
	for _, pty := range ptys {
		pty.Close()
	}

	// Store the command's exit code in the operation's metadata to be returned to caller.
	metadata := shared.Jmap{"return": exitCode}
	opErr := op.UpdateMetadata(metadata)
	if opErr != nil {
		return opErr
	}

	revert.Success()
	return err
}

// swagger:operation POST /1.0/instances/{name}/exec instances instance_exec_post
//
// Run a command
//
// Executes a command inside an instance.
//
// The returned operation metadata will contain either 2 or 4 websockets.
// In non-interactive mode, you'll get one websocket for each of stdin, stdout and stderr.
// In interactive mode, a single bi-directional websocket is used for stdin and stdout/stderr.
//
// An additional "control" socket is always added on top which can be used for out of band communication with LXD.
// This allows sending signals and window sizing information through.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: exec
//     description: Exec request
//     schema:
//       $ref: "#/definitions/InstanceExecPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceExecPost(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
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
	client, err := cluster.ConnectIfInstanceIsRemote(d.cluster, projectName, name, d.endpoints.NetworkCert(), d.serverCert(), r, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		url := fmt.Sprintf("/1.0/instances/%s/exec?project=%s", name, projectName)
		resp, _, err := client.RawQuery("POST", url, post, "")
		if err != nil {
			return response.SmartError(err)
		}

		opAPI, err := resp.MetadataAsOperation()
		if err != nil {
			return response.SmartError(err)
		}

		return operations.ForwardedOperationResponse(projectName, opAPI)
	}

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Instance is not running"))
	}

	if inst.IsFrozen() {
		return response.BadRequest(fmt.Errorf("Instance is frozen"))
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

		if inst.Type() == instancetype.Container {
			// Add some additional paths. This directly looks through /proc
			// rather than use FileExists as none of those paths are expected to be
			// symlinks and this is much faster than forking a sub-process and
			// attaching to the instance.
			extraPaths := map[string]string{
				"/snap":      "/snap/bin",
				"/etc/NIXOS": "/run/current-system/sw/bin",
			}

			initPID := inst.InitPID() // This calls liblxc, so don't do it repeatedly to get same info.
			for k, v := range extraPaths {
				if shared.PathExists(fmt.Sprintf("/proc/%d/root%s", initPID, k)) {
					post.Environment["PATH"] = fmt.Sprintf("%s:%s", post.Environment["PATH"], v)
				}
			}
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
		ws.s = d.State()
		ws.fds = map[int]string{}

		ws.conns = map[int]*websocket.Conn{}
		ws.conns[execWSControl] = nil
		ws.conns[0] = nil // This is used for either TTY or Stdin.

		if !post.Interactive {
			ws.conns[execWSStdout] = nil
			ws.conns[execWSSterr] = nil
		}

		ws.wsControlConnected, ws.wsControlConnectedDone = context.WithCancel(context.Background())
		ws.wsNeededConnected, ws.wsNeededConnectedDone = context.WithCancel(context.Background())

		for i := range ws.conns {
			ws.fds[i], err = shared.RandomCryptoString()
			if err != nil {
				return response.InternalError(err)
			}
		}

		ws.instance = inst
		ws.req = post

		resources := map[string][]string{}
		resources["instances"] = []string{ws.instance.Name()}

		if ws.instance.Type() == instancetype.Container {
			resources["containers"] = resources["instances"]
		}

		op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassWebsocket, db.OperationCommandExec, resources, ws.Metadata(), ws.Do, nil, ws.Connect, r)
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
				"1": fmt.Sprintf("/%s/instances/%s/logs/%s", version.APIVersion, inst.Name(), filepath.Base(stdout.Name())),
				"2": fmt.Sprintf("/%s/instances/%s/logs/%s", version.APIVersion, inst.Name(), filepath.Base(stderr.Name())),
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
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, db.OperationCommandExec, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
