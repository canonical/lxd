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
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type consoleWs struct {
	// container currently worked on
	container container

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

func (s *consoleWs) Connect(op *operation, r *http.Request, w http.ResponseWriter) error {
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

func (s *consoleWs) Do(op *operation) error {
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
				err := syscall.Kill(consolePid, syscall.SIGTERM)
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

			command := api.ContainerConsoleControl{}

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

	consCmd := s.container.Console(slave)
	consCmd.Start()
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
		if status.Signaled() && status.Signal() == syscall.SIGTERM {
			return finisher(nil)
		}
	}

	return finisher(err)
}

func containerConsolePost(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	post := api.ContainerConsolePost{}
	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return BadRequest(err)
	}

	err = json.Unmarshal(buf, &post)
	if err != nil {
		return BadRequest(err)
	}

	// Forward the request if the container is remote.
	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfContainerIsRemote(d.cluster, project, name, cert)
	if err != nil {
		return SmartError(err)
	}

	if client != nil {
		url := fmt.Sprintf("/containers/%s/console?project=%s", name, project)
		op, _, err := client.RawOperation("POST", url, post, "")
		if err != nil {
			return SmartError(err)
		}

		opAPI := op.Get()
		return ForwardedOperationResponse(project, &opAPI)
	}

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	err = fmt.Errorf("Container is not running")
	if !c.IsRunning() {
		return BadRequest(err)
	}

	err = fmt.Errorf("Container is frozen")
	if c.IsFrozen() {
		return BadRequest(err)
	}

	ws := &consoleWs{}
	ws.fds = map[int]string{}

	idmapset, err := c.IdmapSet()
	if err != nil {
		return InternalError(err)
	}

	if idmapset != nil {
		ws.rootUid, ws.rootGid = idmapset.ShiftIntoNs(0, 0)
	}

	ws.conns = map[int]*websocket.Conn{}
	ws.conns[-1] = nil
	ws.conns[0] = nil
	for i := -1; i < len(ws.conns)-1; i++ {
		ws.fds[i], err = shared.RandomCryptoString()
		if err != nil {
			return InternalError(err)
		}
	}

	ws.allConnected = make(chan bool, 1)
	ws.controlConnected = make(chan bool, 1)

	ws.container = c
	ws.width = post.Width
	ws.height = post.Height

	resources := map[string][]string{}
	resources["containers"] = []string{ws.container.Name()}

	op, err := operationCreate(d.cluster, project, operationClassWebsocket, db.OperationConsoleShow,
		resources, ws.Metadata(), ws.Do, nil, ws.Connect)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerConsoleLogGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Forward the request if the container is remote.
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	if !util.RuntimeLiblxcVersionAtLeast(3, 0, 0) {
		return BadRequest(fmt.Errorf("Querying the console buffer requires liblxc >= 3.0"))
	}

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	ent := fileResponseEntry{}
	if !c.IsRunning() {
		// Hand back the contents of the console ringbuffer logfile.
		consoleBufferLogPath := c.ConsoleBufferLogPath()
		ent.path = consoleBufferLogPath
		ent.filename = consoleBufferLogPath
		return FileResponse(r, []fileResponseEntry{ent}, nil, false)
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
			return SmartError(err)
		}

		if errno == syscall.ENODATA {
			return FileResponse(r, []fileResponseEntry{ent}, nil, false)
		}

		return SmartError(err)
	}

	ent.buffer = []byte(logContents)
	return FileResponse(r, []fileResponseEntry{ent}, nil, false)
}

func containerConsoleLogDelete(d *Daemon, r *http.Request) Response {
	if !util.RuntimeLiblxcVersionAtLeast(3, 0, 0) {
		return BadRequest(fmt.Errorf("Clearing the console buffer requires liblxc >= 3.0"))
	}

	name := mux.Vars(r)["name"]
	project := projectParam(r)

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

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

	if !c.IsRunning() {
		consoleLogpath := c.ConsoleBufferLogPath()
		return SmartError(truncateConsoleLogFile(consoleLogpath))
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
			return SmartError(err)
		}

		if errno == syscall.ENODATA {
			return SmartError(nil)
		}

		return SmartError(err)
	}

	return SmartError(nil)
}
