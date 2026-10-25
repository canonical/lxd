package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	liblxc "github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
	"github.com/canonical/lxd/shared/ws"
)

// consoleConnectTimeout is how long an operation waits for the client to connect its websockets.
const consoleConnectTimeout = 10 * time.Second

type consoleWs struct {
	// instance currently worked on
	instance instance.Instance

	// websocket connections to bridge pty fds to
	conns map[int]*websocket.Conn

	// map dynamic websocket connections to their associated console file
	dynamic map[*websocket.Conn]*os.File

	// locks needed to access the "conns" and "dynamic" members
	connsLock sync.Mutex

	// wait until all websockets are properly connected
	allConnected cancel.Canceller

	// wait until the control socket is connected
	controlConnected cancel.Canceller

	// map file descriptors to secret
	fds map[int]string

	// terminal width
	width int

	// terminal height
	height int

	// channel type (either console or vga)
	protocol string

	// track either server or client disconnected
	consoleDone cancel.Canceller
}

// Metadata returns a map of metadata.
func (s *consoleWs) Metadata() map[string]any {
	fds := make(map[string]string, len(s.fds))
	for fd, secret := range s.fds {
		if fd == -1 {
			fds[api.SecretNameControl] = secret
		} else {
			fds[strconv.Itoa(fd)] = secret
		}
	}

	return map[string]any{"fds": fds}
}

// Connect connects to the websocket.
func (s *consoleWs) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	err := op.CheckRequestor(r)
	if err != nil {
		return err
	}

	switch s.protocol {
	case instance.ConsoleTypeConsole:
		return s.connectConsole(r, w)
	case instance.ConsoleTypeVGA:
		return s.connectVGA(r, w)
	default:
		return fmt.Errorf("Unknown protocol %q", s.protocol)
	}
}

func (s *consoleWs) connectConsole(r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return errors.New("missing secret")
	}

	secretBytes := []byte(secret)

	for fd, fdSecret := range s.fds {
		if subtle.ConstantTimeCompare(secretBytes, []byte(fdSecret)) == 1 {
			conn, err := ws.Upgrader.Upgrade(w, r, nil)
			if err != nil {
				return err
			}

			err = s.registerConn(fd, conn)
			if err != nil {
				return err
			}

			ws.StartKeepAlive(conn)

			return nil
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this operation actually exists */
	return os.ErrPermission
}

// registerConn records an upgraded control (-1) or data websocket and signals when it completes the set that
// doConsole or doVGA waits for. It takes ownership of conn and closes it if it cannot be registered.
func (s *consoleWs) registerConn(fd int, conn *websocket.Conn) error {
	s.connsLock.Lock()
	defer s.connsLock.Unlock()

	if s.consoleDone.Err() != nil {
		_ = conn.Close()
		return errors.New("Console operation has finished")
	}

	val, found := s.conns[fd]
	if !found {
		_ = conn.Close()
		return errors.New("Unknown websocket number")
	}

	if val != nil {
		_ = conn.Close()
		return errors.New("Websocket number already connected")
	}

	s.conns[fd] = conn

	if fd == -1 {
		s.controlConnected.Cancel()
		return nil
	}

	for i, c := range s.conns {
		if i != -1 && c == nil {
			return nil
		}
	}

	s.allConnected.Cancel()
	return nil
}

// shutdown marks the console as finished, which makes registerConn and connectVGA reject late connections,
// then closes every websocket and dynamic console. Connections are closed outside the lock.
func (s *consoleWs) shutdown() {
	s.connsLock.Lock()
	s.consoleDone.Cancel()
	s.controlConnected.Cancel()

	conns := make([]*websocket.Conn, 0, len(s.conns))
	for _, conn := range s.conns {
		if conn != nil {
			conns = append(conns, conn)
		}
	}

	dynamic := make(map[*websocket.Conn]*os.File, len(s.dynamic))
	for conn, console := range s.dynamic {
		dynamic[conn] = console
	}

	clear(s.conns)
	clear(s.dynamic)
	s.connsLock.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}

	for conn, console := range dynamic {
		_ = conn.Close()
		_ = console.Close()
	}
}

func (s *consoleWs) connectVGA(r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return errors.New("missing secret")
	}

	secretBytes := []byte(secret)

	for fd, fdSecret := range s.fds {
		if subtle.ConstantTimeCompare(secretBytes, []byte(fdSecret)) != 1 {
			continue
		}

		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return err
		}

		if fd == -1 {
			logger.Debug("VGA control websocket connected")

			err = s.registerConn(fd, conn)
			if err != nil {
				return err
			}

			ws.StartKeepAlive(conn)

			return nil
		}

		logger.Debug("VGA dynamic websocket connected")

		console, _, err := s.instance.Console(r.Context(), "vga")
		if err != nil {
			_ = conn.Close()
			return err
		}

		s.connsLock.Lock()
		if s.consoleDone.Err() != nil {
			s.connsLock.Unlock()
			_ = conn.Close()
			_ = console.Close()
			return errors.New("Console operation has finished")
		}

		s.dynamic[conn] = console
		s.connsLock.Unlock()

		ws.StartKeepAlive(conn)

		// Mirror the console and websocket.
		go func() {
			l := logger.AddContext(logger.Ctx{"address": conn.RemoteAddr().String()})

			defer l.Debug("Finished mirroring websocket")

			l.Debug("Started mirroring websocket")
			readDone, writeDone := ws.Mirror(conn, console)

			for readDone != nil && writeDone != nil {
				select {
				case <-readDone:
					l.Debug("Finished mirroring console to websocket")
					readDone = nil
					s.consoleDone.Cancel()
				case <-writeDone:
					l.Debug("Finished mirroring websocket to console")
					writeDone = nil
					s.consoleDone.Cancel()
				}
			}
		}()

		return nil
	}

	// If we didn't find the right secret, the user provided a bad one,
	// which 403, not 404, since this operation actually exists.
	return os.ErrPermission
}

// Do connects to the websocket and executes the operation.
func (s *consoleWs) Do(ctx context.Context, _ *operations.Operation) error {
	switch s.protocol {
	case instance.ConsoleTypeConsole:
		return s.doConsole(ctx)
	case instance.ConsoleTypeVGA:
		return s.doVGA(ctx)
	default:
		return fmt.Errorf("Unknown protocol %q", s.protocol)
	}
}

func (s *consoleWs) doConsole(ctx context.Context) error {
	defer logger.Debug("Console websocket finished")

	// Deferred first so it runs after the console is closed, which must happen before the websockets are.
	defer s.shutdown()

	logger.Debug("Waiting for console websockets to connect")
	select {
	case <-s.allConnected.Done():
	case <-time.After(consoleConnectTimeout):
		return errors.New("Timed out waiting for websockets to connect")
	case <-ctx.Done():
		return ctx.Err()
	}

	// Get console from instance.
	console, consoleDisconnectCh, err := s.instance.Console(ctx, s.protocol)
	if err != nil {
		return err
	}

	defer func() { _ = console.Close() }()

	// Detect size of window and set it into console.
	if s.width > 0 && s.height > 0 {
		_ = shared.SetSize(int(console.Fd()), s.width, s.height)
	}

	// Wait for control socket to connect and then read messages from the remote side in a loop.
	go func() {
		defer logger.Debug("Console control websocket finished")
		<-s.controlConnected.Done()

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()
		if conn == nil {
			return
		}

		for {
			_, r, err := conn.NextReader()
			if err != nil {
				logger.Debugf("Got error getting next reader: %v", err)
				s.consoleDone.Cancel()
				return
			}

			buf, err := io.ReadAll(r)
			if err != nil {
				logger.Debugf("Failed reading message: %v", err)
				break
			}

			command := api.InstanceConsoleControl{}

			err = json.Unmarshal(buf, &command)
			if err != nil {
				logger.Debugf("Failed unmarshaling control socket command: %s", err)
				continue
			}

			if command.Command == "window-resize" {
				winchWidth, err := strconv.Atoi(command.Args["width"])
				if err != nil {
					logger.Debugf("Cannot extract window width: %s", err)
					continue
				}

				winchHeight, err := strconv.Atoi(command.Args["height"])
				if err != nil {
					logger.Debugf("Cannot extract window height: %s", err)
					continue
				}

				err = shared.SetSize(int(console.Fd()), winchWidth, winchHeight)
				if err != nil {
					logger.Debugf("Failed setting window size to: %dx%d", winchWidth, winchHeight)
					continue
				}

				logger.Debugf("Set window size to: %dx%d", winchWidth, winchHeight)
			}
		}
	}()

	// Mirror the console and websocket.
	s.connsLock.Lock()
	consoleConn := s.conns[0]
	s.connsLock.Unlock()

	mirrorDoneCh := make(chan struct{})
	go func(conn *websocket.Conn) {
		l := logger.AddContext(logger.Ctx{"address": conn.RemoteAddr().String()})
		defer l.Debug("Finished mirroring websocket to console")

		l.Debug("Started mirroring websocket")
		readDone, writeDone := ws.Mirror(conn, console)

		<-readDone
		l.Debug("Finished mirroring console to websocket")
		<-writeDone
		close(mirrorDoneCh)
	}(consoleConn)

	// Wait until either the console, the websocket, or the operation context is done.
	select {
	case <-mirrorDoneCh:
	case <-s.consoleDone.Done():
	case <-ctx.Done():
	}

	close(consoleDisconnectCh)

	// Write a reset escape sequence to the console to cancel any ongoing reads to the handle
	// and then close it. This ordering is important, close the console before closing the
	// websocket to ensure console doesn't get stuck reading.
	_, err = console.Write([]byte("\x1bc"))
	if err != nil {
		_ = console.Close()
		return err
	}

	err = console.Close()
	if err != nil {
		return err
	}

	return nil
}

func (s *consoleWs) doVGA(ctx context.Context) error {
	defer logger.Debug("VGA websocket finished")
	defer s.shutdown()

	// The client connects the control socket before any dynamic one, so wait for it to ensure abandoned operations end.
	select {
	case <-s.controlConnected.Done():
	case <-time.After(consoleConnectTimeout):
		return errors.New("Timed out waiting for control websocket to connect")
	case <-ctx.Done():
		return ctx.Err()
	}

	// The control socket is used to terminate the operation.
	s.connsLock.Lock()
	control := s.conns[-1]
	s.connsLock.Unlock()

	go func() {
		defer logger.Debug("VGA control websocket finished")

		for {
			_, _, err := control.NextReader()
			if err != nil {
				logger.Debugf("Got error getting next reader: %v", err)
				s.consoleDone.Cancel()
				return
			}
		}
	}()

	// Wait until the control channel is done or the context is cancelled.
	select {
	case <-s.consoleDone.Done():
	case <-ctx.Done():
	}

	return nil
}

// swagger:operation POST /1.0/instances/{name}/console instances instance_console_post
//
//	Connect to console
//
//	Connects to the console of an instance.
//
//	The returned operation metadata will contain two websockets, one for data and one for control.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: console
//	    description: Console request
//	    schema:
//	      $ref: "#/definitions/InstanceConsolePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceConsolePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name := r.PathValue("name")
	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	post := api.InstanceConsolePost{}
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	err = json.Unmarshal(buf, &post)
	if err != nil {
		return response.BadRequest(err)
	}

	// Forward the request if the container is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		url := api.NewURL().Path(version.APIVersion, "instances", name, "console").Project(projectName)
		opAPI, err := requestOperation(client, url, post)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedOperationResponse(opAPI)
	}

	if post.Type == "" {
		post.Type = instance.ConsoleTypeConsole
	}

	// Basic parameter validation.
	if !slices.Contains([]string{instance.ConsoleTypeConsole, instance.ConsoleTypeVGA}, post.Type) {
		return response.BadRequest(fmt.Errorf("Unknown console type %q", post.Type))
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if post.Type == instance.ConsoleTypeVGA && inst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("VGA console is only supported by virtual machines"))
	}

	if !inst.IsRunning() {
		return response.BadRequest(errors.New("Instance is not running"))
	}

	if inst.IsFrozen() {
		return response.BadRequest(errors.New("Instance is frozen"))
	}

	ws := &consoleWs{}
	ws.fds = map[int]string{}
	ws.conns = map[int]*websocket.Conn{}
	ws.conns[-1] = nil
	ws.conns[0] = nil
	ws.consoleDone = cancel.New()
	ws.dynamic = map[*websocket.Conn]*os.File{}
	for i := -1; i < len(ws.conns)-1; i++ {
		ws.fds[i], err = shared.RandomCryptoString()
		if err != nil {
			return response.InternalError(err)
		}
	}

	ws.allConnected = cancel.New()
	ws.controlConnected = cancel.New()
	ws.instance = inst
	ws.width = post.Width
	ws.height = post.Height
	ws.protocol = post.Type

	instanceURL := api.NewURL().Path(version.APIVersion, "instances", ws.instance.Name()).Project(projectName)
	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   instanceURL,
		Type:        operationtype.ConsoleShow,
		Class:       operationtype.OperationClassWebsocket,
		Metadata:    ws.Metadata(),
		RunHook:     ws.Do,
		ConnectHook: ws.Connect,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/console instances instance_console_get
//
//	Get console log
//
//	Gets the console log for the instance.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw console log
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceConsoleLogGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, _, _, resp := forwardedInstanceResponseWithInstance(s, r)
	if resp != nil {
		return resp
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(errors.New("Instance is not container type"))
	}

	c, ok := inst.(instance.Container)
	if !ok {
		return response.SmartError(errors.New("Invalid instance type"))
	}

	ent := response.FileResponseEntry{}
	if !c.IsRunning() {
		// Hand back the contents of the console ringbuffer logfile.
		consoleBufferLogPath := c.ConsoleBufferLogPath()
		ent.Path = consoleBufferLogPath
		ent.Filename = consoleBufferLogPath
		return response.FileResponse([]response.FileResponseEntry{ent}, nil)
	}

	// Query the container's console ringbuffer.
	console := liblxc.ConsoleLogOptions{
		ClearLog:       false,
		ReadLog:        true,
		ReadMax:        0,
		WriteToLogFile: true,
	}

	// Send a ringbuffer request to the container.
	logContents, err := c.ConsoleLog(r.Context(), console)
	if err != nil {
		errno, isErrno := shared.GetErrno(err)
		if !isErrno {
			return response.SmartError(err)
		}

		if errno == unix.ENODATA {
			return response.FileResponse([]response.FileResponseEntry{}, nil)
		}

		return response.SmartError(err)
	}

	ent.File = bytes.NewReader([]byte(logContents))
	ent.FileModified = time.Now()
	ent.FileSize = int64(len(logContents))

	return response.FileResponse([]response.FileResponseEntry{ent}, nil)
}

// swagger:operation DELETE /1.0/instances/{name}/console instances instance_console_delete
//
//	Clear the console log
//
//	Clears the console log buffer.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceConsoleLogDelete(d *Daemon, r *http.Request) response.Response {
	name := r.PathValue("name")
	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	projectName := request.ProjectParam(r)

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(errors.New("Instance is not container type"))
	}

	c, ok := inst.(instance.Container)
	if !ok {
		return response.SmartError(errors.New("Invalid instance type"))
	}

	if !inst.IsRunning() {
		consoleLogpath := c.ConsoleBufferLogPath()
		if consoleLogpath == "" {
			return response.SmartError(errors.New("Container does not keep a console logfile"))
		}

		// Check that this is a regular file. We don't want to try and unlink
		// /dev/stderr or /dev/null or something.
		st, err := os.Stat(consoleLogpath)
		if err != nil {
			return response.SmartError(err)
		}

		if !st.Mode().IsRegular() {
			return response.SmartError(errors.New("The console log is not a regular file"))
		}

		return response.SmartError(os.Truncate(consoleLogpath, 0))
	}

	// Send a ringbuffer request to the container.
	console := liblxc.ConsoleLogOptions{
		ClearLog:       true,
		ReadLog:        false,
		ReadMax:        0,
		WriteToLogFile: false,
	}

	_, err = c.ConsoleLog(r.Context(), console)
	if err != nil {
		errno, isErrno := shared.GetErrno(err)
		if !isErrno {
			return response.SmartError(err)
		}

		if errno == unix.ENODATA {
			return response.SmartError(nil)
		}

		return response.SmartError(err)
	}

	return response.SmartError(nil)
}
