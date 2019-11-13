package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type consoleWs struct {
	// instance currently worked on
	instance Instance

	// websocket connections to bridge pty fds to
	conns map[int]*websocket.Conn

	// locks needed to access the "conns" member
	connsLock sync.Mutex

	// channel to wait until all websockets are properly connected
	allConnected chan bool

	// channel to wait until the control socket is connected
	controlConnected chan bool

	// map file descriptors to secret
	fds map[int]string

	// terminal width
	width int

	// terminal height
	height int
}

func (s *consoleWs) Metadata() interface{} {
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

func (s *consoleWs) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
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

func (s *consoleWs) Do(op *operations.Operation) error {
	defer logger.Debug("Console websocket finished")
	<-s.allConnected

	// Get console from instance.
	console, err := s.instance.Console()
	if err != nil {
		return err
	}
	defer console.Close()

	// Switch the console file descriptor into raw mode.
	oldttystate, err := termios.MakeRaw(int(console.Fd()))
	if err != nil {
		return err
	}
	defer termios.Restore(int(console.Fd()), oldttystate)

	// Detect size of window and set it into console.
	if s.width > 0 && s.height > 0 {
		shared.SetSize(int(console.Fd()), s.width, s.height)
	}

	consoleDoneCh := make(chan struct{})

	// Wait for control socket to connect and then read messages from the remote side in a loop.
	go func() {
		defer logger.Debugf("Console control websocket finished")
		res := <-s.controlConnected
		if !res {
			return
		}

		for {
			s.connsLock.Lock()
			conn := s.conns[-1]
			s.connsLock.Unlock()

			_, r, err := conn.NextReader()
			if err != nil {
				logger.Debugf("Got error getting next reader %s", err)
				close(consoleDoneCh)
				return
			}

			buf, err := ioutil.ReadAll(r)
			if err != nil {
				logger.Debugf("Failed to read message %s", err)
				break
			}

			command := api.InstanceConsoleControl{}

			err = json.Unmarshal(buf, &command)
			if err != nil {
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

				err = shared.SetSize(int(console.Fd()), winchWidth, winchHeight)
				if err != nil {
					logger.Debugf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
					continue
				}

				logger.Debugf("Set window size to: %dx%d", winchWidth, winchHeight)
			}
		}
	}()

	// Mirror the console and websocket.
	mirrorDoneCh := make(chan struct{})
	go func() {
		defer logger.Debugf("Finished mirroring websocket to console")
		s.connsLock.Lock()
		conn := s.conns[0]
		s.connsLock.Unlock()

		logger.Debugf("Starting mirroring websocket")
		readDone, writeDone := shared.WebsocketConsoleMirror(conn, console, console)

		<-readDone
		logger.Debugf("Finished mirroring console to websocket")
		<-writeDone
		conn.Close()
		close(mirrorDoneCh)
	}()

	// Wait until either the console or the websocket is done.
	select {
	case <-mirrorDoneCh:
	case <-consoleDoneCh:
	}

	// Write a reset escape sequence to the console to cancel any ongoing reads to the handle
	// and then close it. This ordering is important, close the console before closing the
	// websocket to ensure console doesn't get stuck reading.
	console.Write([]byte("\x1bc"))
	console.Close()

	// Get the console websocket and close it.
	s.connsLock.Lock()
	consolConn := s.conns[0]
	s.connsLock.Unlock()
	consolConn.Close()

	// Get the control websocket and close it.
	s.connsLock.Lock()
	ctrlConn := s.conns[-1]
	s.connsLock.Unlock()
	ctrlConn.Close()

	// Indicate to the control socket go routine to end if not already.
	close(s.controlConnected)
	return nil
}

func containerConsolePost(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	post := api.InstanceConsolePost{}
	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	err = json.Unmarshal(buf, &post)
	if err != nil {
		return response.BadRequest(err)
	}

	// Forward the request if the container is remote.
	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfContainerIsRemote(d.cluster, project, name, cert, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		url := fmt.Sprintf("/containers/%s/console?project=%s", name, project)
		op, _, err := client.RawOperation("POST", url, post, "")
		if err != nil {
			return response.SmartError(err)
		}

		opAPI := op.Get()
		return operations.ForwardedOperationResponse(project, &opAPI)
	}

	inst, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	err = fmt.Errorf("Container is not running")
	if !inst.IsRunning() {
		return response.BadRequest(err)
	}

	err = fmt.Errorf("Container is frozen")
	if inst.IsFrozen() {
		return response.BadRequest(err)
	}

	ws := &consoleWs{}
	ws.fds = map[int]string{}
	ws.conns = map[int]*websocket.Conn{}
	ws.conns[-1] = nil
	ws.conns[0] = nil
	for i := -1; i < len(ws.conns)-1; i++ {
		ws.fds[i], err = shared.RandomCryptoString()
		if err != nil {
			return response.InternalError(err)
		}
	}

	ws.allConnected = make(chan bool, 1)
	ws.controlConnected = make(chan bool, 1)
	ws.instance = inst
	ws.width = post.Width
	ws.height = post.Height

	resources := map[string][]string{}
	resources["instances"] = []string{ws.instance.Name()}
	resources["containers"] = resources["instances"] // Old field name.

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassWebsocket, db.OperationConsoleShow,
		resources, ws.Metadata(), ws.Do, nil, ws.Connect)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func containerConsoleLogGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Forward the request if the container is remote.
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	if !util.RuntimeLiblxcVersionAtLeast(3, 0, 0) {
		return response.BadRequest(fmt.Errorf("Querying the console buffer requires liblxc >= 3.0"))
	}

	inst, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	c := inst.(container)
	ent := response.FileResponseEntry{}
	if !c.IsRunning() {
		// Hand back the contents of the console ringbuffer logfile.
		consoleBufferLogPath := c.ConsoleBufferLogPath()
		ent.Path = consoleBufferLogPath
		ent.Filename = consoleBufferLogPath
		return response.FileResponse(r, []response.FileResponseEntry{ent}, nil, false)
	}

	// Query the container's console ringbuffer.
	console := lxc.ConsoleLogOptions{
		ClearLog:       false,
		ReadLog:        true,
		ReadMax:        0,
		WriteToLogFile: true,
	}

	// Send a ringbuffer request to the container.
	logContents, err := c.ConsoleLog(console)
	if err != nil {
		errno, isErrno := shared.GetErrno(err)
		if !isErrno {
			return response.SmartError(err)
		}

		if errno == unix.ENODATA {
			return response.FileResponse(r, []response.FileResponseEntry{ent}, nil, false)
		}

		return response.SmartError(err)
	}

	ent.Buffer = []byte(logContents)
	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil, false)
}

func containerConsoleLogDelete(d *Daemon, r *http.Request) response.Response {
	if !util.RuntimeLiblxcVersionAtLeast(3, 0, 0) {
		return response.BadRequest(fmt.Errorf("Clearing the console buffer requires liblxc >= 3.0"))
	}

	name := mux.Vars(r)["name"]
	project := projectParam(r)

	inst, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	c := inst.(container)

	truncateConsoleLogFile := func(path string) error {
		// Check that this is a regular file. We don't want to try and unlink
		// /dev/stderr or /dev/null or something.
		st, err := os.Stat(path)
		if err != nil {
			return err
		}

		if !st.Mode().IsRegular() {
			return fmt.Errorf("The console log is not a regular file")
		}

		if path == "" {
			return fmt.Errorf("Container does not keep a console logfile")
		}

		return os.Truncate(path, 0)
	}

	if !inst.IsRunning() {
		consoleLogpath := c.ConsoleBufferLogPath()
		return response.SmartError(truncateConsoleLogFile(consoleLogpath))
	}

	// Send a ringbuffer request to the container.
	console := lxc.ConsoleLogOptions{
		ClearLog:       true,
		ReadLog:        false,
		ReadMax:        0,
		WriteToLogFile: false,
	}

	_, err = c.ConsoleLog(console)
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
