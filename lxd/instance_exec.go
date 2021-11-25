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

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

type execWs struct {
	req api.InstanceExecPost

	instance         instance.Instance
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan struct{}
	allConnectedDone bool
	fds              map[int]string
	s                *state.State
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
			logger.Error("tomp fd connected", "fd", fd)

			if s.allConnectedDone {
				return fmt.Errorf("All websockets already connected")
			}

			for _, c := range s.conns {
				if c == nil {
					// Still more needed to connect.
					s.connsLock.Unlock()
					return nil
				}
			}

			// All WS now connected.
			logger.Error("tomp all connected", "conns", len(s.conns))
			s.allConnectedDone = true
			close(s.allConnected)

			s.connsLock.Unlock()

			return nil
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this operation actually exists */
	return os.ErrPermission
}

func (s *execWs) Do(op *operations.Operation) error {
	<-s.allConnected

	exitCode, err := s.instance.Exec(s.req, s.conns, nil, nil)
	if err != nil {
		return err
	}

	metadata := shared.Jmap{"return": exitCode}
	opErr := op.UpdateMetadata(metadata)
	if opErr != nil {
		return opErr
	}

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

		// Add some additional paths. This directly looks through /proc
		// rather than use FileExists as none of those paths are expected to be
		// symlinks and this is much faster than forking a sub-process and
		// attaching to the instance.

		extraPaths := map[string]string{
			"/snap":      "/snap/bin",
			"/etc/NIXOS": "/run/current-system/sw/bin",
		}

		for k, v := range extraPaths {
			if shared.PathExists(fmt.Sprintf("/proc/%d/root%s", inst.InitPID(), k)) {
				post.Environment["PATH"] = fmt.Sprintf("%s:%s", post.Environment["PATH"], v)
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
		if post.Interactive {
			ws.conns[-1] = nil
			ws.conns[0] = nil
		} else {
			ws.conns[0] = nil
			ws.conns[1] = nil
			ws.conns[2] = nil
		}

		ws.allConnected = make(chan struct{})

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
			exitCode, err := inst.Exec(post, nil, stdout, stderr)
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
			exitCode, err := inst.Exec(post, nil, nil, nil)
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
