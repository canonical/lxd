package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
)

type ttyWs struct {
	container        container
	conns            map[int]*websocket.Conn
	connsLock        sync.Mutex
	allConnected     chan bool
	controlConnected chan bool
	fds              map[int]string
	interactive      bool
	width            int
	height           int
}

func newttyWs(c container, interactive bool, width int, height int) (*ttyWs, error) {
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
		interactive:      interactive,
		width:            width,
		height:           height,
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
