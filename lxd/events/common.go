package events

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/shared/logger"
)

// serverCommon represents an instance of a comon event server.
type serverCommon struct {
	debug   bool
	verbose bool
	lock    sync.Mutex
}

// listenerCommon describes a common event listener.
type listenerCommon struct {
	*websocket.Conn

	messageTypes []string
	ctx          context.Context
	ctxCancel    func()
	id           string
	lock         sync.Mutex
	pongsPending uint

	// If true, this listener won't get events forwarded from other
	// nodes. It only used by listeners created internally by LXD nodes
	// connecting to other LXD nodes to get their local events only.
	localOnly bool
}

func (e *listenerCommon) heartbeat() {
	logger.Debug("Event listener server handler started", log.Ctx{"listener": e.ID(), "local": e.Conn.LocalAddr(), "remote": e.Conn.RemoteAddr(), "localOnly": e.localOnly})

	defer e.Close()

	pingInterval := time.Second * 5
	e.pongsPending = 0

	e.SetPongHandler(func(msg string) error {
		e.lock.Lock()
		e.pongsPending = 0
		e.lock.Unlock()
		return nil
	})

	// Run a blocking reader to detect if the remote side is closed.
	// We don't expect to get anything from the remote side, so this should remain blocked until disconnected.
	go func() {
		e.Conn.NextReader()
		e.Close()
	}()

	for {
		if e.IsClosed() {
			return
		}

		e.lock.Lock()
		if e.pongsPending > 2 {
			e.lock.Unlock()
			logger.Warn("Hearbeat for event listener handler timed out", log.Ctx{"listener": e.ID(), "local": e.Conn.LocalAddr(), "remote": e.Conn.RemoteAddr(), "localOnly": e.localOnly})
			return
		}
		err := e.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(5*time.Second))
		if err != nil {
			e.lock.Unlock()
			return
		}

		e.pongsPending++
		e.lock.Unlock()

		select {
		case <-time.After(pingInterval):
		case <-e.ctx.Done():
			return
		}
	}
}

// IsClosed returns true if the listener is closed.
func (e *listenerCommon) IsClosed() bool {
	return e.ctx.Err() != nil
}

// ID returns the listener ID.
func (e *listenerCommon) ID() string {
	return e.id
}

// Wait waits for a message on its active channel or the context is cancelled, then returns.
func (e *listenerCommon) Wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-e.ctx.Done():
	}
}

// Close Disconnects the listener.
func (e *listenerCommon) Close() {
	e.lock.Lock()
	defer e.lock.Unlock()

	if e.IsClosed() {
		return
	}

	logger.Debug("Event listener server handler stopped", log.Ctx{"listener": e.ID(), "local": e.Conn.LocalAddr(), "remote": e.Conn.RemoteAddr(), "localOnly": e.localOnly})

	e.Conn.Close()
	e.ctxCancel()
}

// WriteJSON message to the connection.
func (e *listenerCommon) WriteJSON(v interface{}) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	return e.Conn.WriteJSON(v)
}

// WriteMessage to the connection.
func (e *listenerCommon) WriteMessage(messageType int, data []byte) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	return e.Conn.WriteMessage(messageType, data)
}
