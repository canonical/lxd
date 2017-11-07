package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type ttyWsOps interface {
	openTTYs(s *ttyWs) (*os.File, *os.File, *os.File, error)
	startup(s *ttyWs)
	do(s *ttyWs, stdin, stdout, stderr *os.File) error
	handleSignal(s *ttyWs, signal int)
	handleAbnormalClosure(s *ttyWs)
	getMetadata(s *ttyWs) shared.Jmap
}

type ttyWs struct {
	container        container
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
	controlExit      chan bool
	doneCh           chan bool
	wgEOF            sync.WaitGroup
	interactive      bool
	fds              map[int]string
	ttys             []*os.File
	ptys             []*os.File
	width            int
	height           int

	ops ttyWsOps
}

func newttyWs(ops ttyWsOps, c container, interactive bool, width int, height int) (*ttyWs, error) {
	fds := map[int]string{}
	conns := map[int]*websocket.Conn{
		-1: nil,
		0:  nil,
	}
	if !interactive {
		conns[1] = nil
		conns[2] = nil
	}
	for i := -1; i < len(conns)-1; i++ {
		var err error
		fds[i], err = shared.RandomCryptoString()
		if err != nil {
			return nil, err
		}
	}

	return &ttyWs{
		container:        c,
		fds:              fds,
		conns:            conns,
		allConnected:     make(chan bool, 1),
		controlConnected: make(chan bool, 1),
		controlExit:      make(chan bool),
		doneCh:           make(chan bool, 1),
		interactive:      interactive,
		width:            width,
		height:           height,
		ops:              ops,
	}, nil
}

func (s *ttyWs) Metadata() interface{} {
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

func (s *ttyWs) Connect(op *operation, r *http.Request, w http.ResponseWriter) error {
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

func (s *ttyWs) Do(op *operation) error {
	<-s.allConnected

	stdin, stdout, stderr, err := s.ops.openTTYs(s)
	if err != nil {
		return err
	}

	if s.interactive {
		s.wgEOF.Add(1)
		go func() {
			s.ops.startup(s)
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

					s.ops.handleAbnormalClosure(s)
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
					s.ops.handleSignal(s, command.Signal)
				}
			}
		}()
		s.connectInteractiveStreams()
	} else {
		s.connectNotInteractiveStreams()
	}

	err = s.ops.do(s, stdin, stdout, stderr)
	if err != nil {
		return err
	}
	s.finish(op)
	return op.UpdateMetadata(s.ops.getMetadata(s))
}

func (s *ttyWs) connectInteractiveStreams() {
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

func (s *ttyWs) connectNotInteractiveStreams() {
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

func (s *ttyWs) finish(op *operation) {
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
