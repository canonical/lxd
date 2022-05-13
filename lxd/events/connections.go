package events

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/api"
)

// EventListenerConnection represents an event listener connection.
type EventListenerConnection interface {
	Reader(ctx context.Context, recvFunc EventHandler)
	WriteJSON(event any) error
	Close() error
	LocalAddr() net.Addr  // Used for logging
	RemoteAddr() net.Addr // Used for logging
}

type websockListenerConnection struct {
	*websocket.Conn

	lock         sync.Mutex
	pongsPending int
}

// NewWebsocketListenerConnection returns a new websocket listener connection.
func NewWebsocketListenerConnection(connection *websocket.Conn) EventListenerConnection {
	return &websockListenerConnection{
		Conn: connection,
	}
}

func (e *websockListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	ctx, cancel := context.WithCancel(ctx)

	close := func() {
		e.lock.Lock()
		defer e.lock.Unlock()

		if ctx.Err() != nil {
			return
		}

		_ = e.Close()
		cancel()
	}

	defer close()

	pingInterval := time.Second * 10
	e.pongsPending = 0

	e.SetPongHandler(func(msg string) error {
		e.lock.Lock()
		e.pongsPending = 0
		e.lock.Unlock()
		return nil
	})

	// Start reader from client.
	go func() {
		defer close()

		if recvFunc != nil {
			for {
				var event api.Event
				err := e.Conn.ReadJSON(&event)
				if err != nil {
					return // This detects if client has disconnected or sent invalid data.
				}

				// Pass received event to the handler.
				recvFunc(event)
			}
		} else {
			// Run a blocking reader to detect if the client has disconnected. We don't expect to get
			// anything from the remote side, so this should remain blocked until disconnected.
			_, _, _ = e.Conn.NextReader()
		}
	}()

	for {
		if ctx.Err() != nil {
			return
		}

		e.lock.Lock()
		if e.pongsPending > 2 {
			e.lock.Unlock()
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
		case <-ctx.Done():
			return
		}
	}
}

func (e *websockListenerConnection) WriteJSON(event any) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	err := e.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return fmt.Errorf("Failed setting write deadline: %w", err)
	}

	return e.Conn.WriteJSON(event)
}
