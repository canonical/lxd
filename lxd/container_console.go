package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

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
)

type consoleWs struct {
	// instance currently worked on
	instance Instance

	// uid to chown pty to
	rootUid int64

	// gid to chown pty to
	rootGid int64

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
	<-s.allConnected

	var err error
	master := &os.File{}
	slave := &os.File{}
	master, slave, err = shared.OpenPty(s.rootUid, s.rootGid)
	if err != nil {
		return err
	}

	if s.width > 0 && s.height > 0 {
		shared.SetSize(int(master.Fd()), s.width, s.height)
	}

	controlExit := make(chan bool)
	var wgEOF sync.WaitGroup

	consolePidChan := make(chan int)
	wgEOF.Add(1)
	go func() {
		select {
		case <-s.controlConnected:
			break

		case <-controlExit:
			return
		}

		consolePid := <-consolePidChan

		for {
			s.connsLock.Lock()
			conn := s.conns[-1]
			s.connsLock.Unlock()

			_, r, err := conn.NextReader()
			if err != nil {
				logger.Debugf("Got error getting next reader %s", err)
				err := unix.Kill(consolePid, unix.SIGTERM)
				if err != nil {
					logger.Debugf("Failed to send SIGTERM to pid %d", consolePid)
				} else {
					logger.Debugf("Sent SIGTERM to pid %d", consolePid)
				}
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

				err = shared.SetSize(int(master.Fd()), winchWidth, winchHeight)
				if err != nil {
					logger.Debugf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
					continue
				}

				logger.Debugf("Set window size to: %dx%d", winchWidth, winchHeight)
			}
		}
	}()

	go func() {
		s.connsLock.Lock()
		conn := s.conns[0]
		s.connsLock.Unlock()

		logger.Debugf("Starting to mirror websocket")
		readDone, writeDone := shared.WebsocketConsoleMirror(conn, master, master)

		<-readDone
		logger.Debugf("Finished to read websocket")
		<-writeDone
		logger.Debugf("Finished to write websocket")
		logger.Debugf("Finished to mirror websocket")

		conn.Close()
		wgEOF.Done()
	}()

	finisher := func(cmdErr error) error {
		slave.Close()

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()

		if conn == nil {
			controlExit <- true
		}

		wgEOF.Wait()

		master.Close()

		return cmdErr
	}

	consCmd := s.instance.Console(slave)
	if consCmd == nil {
		return fmt.Errorf("Failed to start console")
	}

	err = consCmd.Start()
	if err != nil {
		return err
	}

	consolePidChan <- consCmd.Process.Pid
	err = consCmd.Wait()
	if err == nil {
		return finisher(nil)
	}

	exitErr, ok := err.(*exec.ExitError)
	if ok {
		status, _ := exitErr.Sys().(syscall.WaitStatus)
		// If we received SIGTERM someone told us to detach from the
		// console.
		if status.Signaled() && status.Signal() == unix.SIGTERM {
			return finisher(nil)
		}
	}

	return finisher(err)
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

	// If the type of instance is container, setup the root UID/GID for web socket.
	if inst.Type() == instancetype.Container {
		c := inst.(container)
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
	resources["containers"] = []string{ws.instance.Name()}

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
