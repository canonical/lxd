package events

import (
	"context"
	"sync"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// EventHandler called when the connection receives an event from the client.
type EventHandler func(event api.Event)

// serverCommon represents an instance of a comon event server.
type serverCommon struct {
	debug   bool
	verbose bool
	lock    sync.Mutex
}

// listenerCommon describes a common event listener.
type listenerCommon struct {
	EventListenerConnection

	messageTypes []string
	ctx          context.Context
	ctxCancel    func()
	id           string
	lock         sync.Mutex
	pongsPending uint
	recvFunc     EventHandler
}

func (e *listenerCommon) start() {
	logger.Debug("Event listener server handler started", logger.Ctx{"id": e.id, "local": e.LocalAddr(), "remote": e.RemoteAddr()})

	e.Reader(e.ctx, e.recvFunc)
	e.Close()
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

	logger.Debug("Event listener server handler stopped", logger.Ctx{"listener": e.ID(), "local": e.LocalAddr(), "remote": e.RemoteAddr()})

	err := e.EventListenerConnection.Close()
	if err != nil {
		logger.Error("Failed closing listener connection", logger.Ctx{"listener": e.ID(), "err": err})
	}
	e.ctxCancel()
}
