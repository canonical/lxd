package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/netutils"
	"golang.org/x/sys/unix"
)

var execCmd = APIEndpoint{
	Name: "exec",
	Path: "exec",

	Post: APIEndpointAction{Handler: execPost},
}

func execPost(r *http.Request) response.Response {
	post := api.ContainerExecPost{}

	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	if err := json.Unmarshal(buf, &post); err != nil {
		return response.BadRequest(err)
	}

	env := map[string]string{}

	if post.Environment != nil {
		for k, v := range post.Environment {
			env[k] = v
		}
	}
	// Set default value for PATH
	_, ok := env["PATH"]
	if !ok {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	// If running as root, set some env variables
	if post.User == 0 {
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
	}

	// Set default value for LANG
	_, ok = env["LANG"]
	if !ok {
		env["LANG"] = "C.UTF-8"
	}

	ws := &execWs{}
	ws.fds = map[int]string{}

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
	for i := -1; i < len(ws.conns)-1; i++ {
		ws.fds[i], err = shared.RandomCryptoString()
		if err != nil {
			return response.InternalError(err)
		}
	}

	ws.command = post.Command
	ws.env = env

	ws.width = post.Width
	ws.height = post.Height

	ws.cwd = post.Cwd
	ws.uid = post.User
	ws.gid = post.Group

	resources := map[string][]string{}

	op, err := operations.OperationCreate(nil, "", operations.OperationClassWebsocket, db.OperationCommandExec, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

type execWs struct {
	command []string
	env     map[string]string

	rootUid          int64
	rootGid          int64
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
	interactive      bool
	fds              map[int]string
	width            int
	height           int
	uid              uint32
	gid              uint32
	cwd              string
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
		"command":     s.command,
		"environment": s.env,
		"interactive": s.interactive,
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
	 * which 403, not 404, since this Operation actually exists */
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

	if s.interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)
		ptys[0], ttys[0], err = shared.OpenPty(s.rootUid, s.rootGid)
		if err != nil {
			return err
		}

		stdin = ttys[0]
		stdout = ttys[0]
		stderr = ttys[0]

		if s.width > 0 && s.height > 0 {
			shared.SetSize(int(ptys[0].Fd()), s.width, s.height)
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
	attachedChildIsBorn := make(chan int)
	attachedChildIsDead := make(chan bool, 1)
	var wgEOF sync.WaitGroup

	if s.interactive {
		wgEOF.Add(1)
		go func() {
			attachedChildPid := <-attachedChildIsBorn
			select {
			case <-s.controlConnected:
				break

			case <-controlExit:
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
					log.Printf("Got error getting next reader %s", err)
					er, ok := err.(*websocket.CloseError)
					if !ok {
						break
					}

					if er.Code != websocket.CloseAbnormalClosure {
						break
					}

					// If an abnormal closure occurred, kill the attached process.
					err := unix.Kill(attachedChildPid, unix.SIGKILL)
					if err != nil {
						log.Printf("Failed to send SIGKILL to pid %d\n", attachedChildPid)
					} else {
						log.Printf("Sent SIGKILL to pid %d\n", attachedChildPid)
					}
					return
				}

				buf, err := ioutil.ReadAll(r)
				if err != nil {
					log.Printf("Failed to read message %s\n", err)
					break
				}

				command := api.ContainerExecControl{}

				if err := json.Unmarshal(buf, &command); err != nil {
					log.Printf("Failed to unmarshal control socket command: %s\n", err)
					continue
				}

				if command.Command == "window-resize" {
					winchWidth, err := strconv.Atoi(command.Args["width"])
					if err != nil {
						log.Printf("Unable to extract window width: %s\n", err)
						continue
					}

					winchHeight, err := strconv.Atoi(command.Args["height"])
					if err != nil {
						log.Printf("Unable to extract window height: %s\n", err)
						continue
					}

					err = shared.SetSize(int(ptys[0].Fd()), winchWidth, winchHeight)
					if err != nil {
						log.Printf("Failed to set window size to: %dx%d\n", winchWidth, winchHeight)
						continue
					}
				} else if command.Command == "signal" {
					if err := unix.Kill(attachedChildPid, unix.Signal(command.Signal)); err != nil {
						log.Printf("Failed forwarding signal '%d' to PID %d\n", command.Signal, attachedChildPid)
						continue
					}
					log.Printf("Forwarded signal '%d' to PID %d\n", command.Signal, attachedChildPid)
				}
			}
		}()

		go func() {
			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			log.Println("Starting to mirror websocket")
			readDone, writeDone := netutils.WebsocketExecMirror(conn, ptys[0], ptys[0], attachedChildIsDead, int(ptys[0].Fd()))

			<-readDone
			<-writeDone
			log.Println("Finished to mirror websocket")

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

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()

		if conn == nil {
			if s.interactive {
				controlExit <- true
			}
		} else {
			conn.Close()
		}

		attachedChildIsDead <- true

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

	var cmd *exec.Cmd

	if len(s.command) > 1 {
		cmd = exec.Command(s.command[0], s.command[1:]...)
	} else {
		cmd = exec.Command(s.command[0])
	}

	// Prepare the environment
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Start()
	if err != nil {
		return finisher(-1, err)
	}

	err = cmd.Wait()
	if err == nil {
		return finisher(0, nil)
	}

	exitErr, ok := err.(*exec.ExitError)
	if ok {
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if ok {
			return finisher(status.ExitStatus(), nil)
		}

		if status.Signaled() {
			// 128 + n == Fatal error signal "n"
			return finisher(128+int(status.Signal()), nil)
		}
	}

	return finisher(-1, nil)
}
