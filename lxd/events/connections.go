package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
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

type streamListenerConnection struct {
	net.Conn

	lock sync.Mutex
}

type simpleListenerConnection struct {
	rwc io.ReadWriteCloser

	lock sync.Mutex
}

// readerCommon implements the common reader logic for stream and simple connections.
func readerCommon(ctx context.Context, lock *sync.Mutex, rc io.ReadCloser) {
	ctx, cancelFunc := context.WithCancel(ctx)

	closeFunc := func() {
		lock.Lock()
		defer lock.Unlock()

		if ctx.Err() != nil {
			return
		}

		err := rc.Close()
		if err != nil {
			logger.Warn("Failed closing connection", logger.Ctx{"err": err})
		}

		cancelFunc()
	}

	defer closeFunc()

	// Start reader from client.
	go func() {
		defer closeFunc()

		buf := make([]byte, 1)

		// This is used to determine whether the client has terminated.
		_, err := rc.Read(buf)
		if err != nil && errors.Is(err, io.EOF) {
			return
		}
	}()

	if ctx.Err() != nil {
		return
	}

	<-ctx.Done()
}

// NewWebsocketListenerConnection returns a new websocket listener connection.
func NewWebsocketListenerConnection(connection *websocket.Conn) EventListenerConnection {
	return &websockListenerConnection{
		Conn: connection,
	}
}

// Reader for the websocket connection.
func (e *websockListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	ctx, cancel := context.WithCancel(ctx)

	closer := func() {
		e.lock.Lock()
		defer e.lock.Unlock()

		if ctx.Err() != nil {
			return
		}

		_ = e.Close()
		cancel()
	}

	defer closer()

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
		defer closer()

		if recvFunc != nil {
			for {
				var event api.Event
				err := e.ReadJSON(&event)
				if err != nil {
					return // This detects if client has disconnected or sent invalid data.
				}

				// Pass received event to the handler.
				recvFunc(event)
			}
		} else {
			// Run a blocking reader to detect if the client has disconnected. We don't expect to get
			// anything from the remote side, so this should remain blocked until disconnected.
			_, _, _ = e.NextReader()
		}
	}()

	t := time.NewTicker(pingInterval)
	defer t.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		e.lock.Lock()
		if e.pongsPending > 2 {
			e.lock.Unlock()
			return
		}

		err := e.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		if err != nil {
			e.lock.Unlock()
			return
		}

		e.pongsPending++
		e.lock.Unlock()

		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

// WriteJSON sends a JSON event to the websocket connection.
func (e *websockListenerConnection) WriteJSON(event any) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	err := e.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return fmt.Errorf("Failed setting write deadline: %w", err)
	}

	return e.Conn.WriteJSON(event)
}

// NewStreamListenerConnection returns a new http stream listener connection.
func NewStreamListenerConnection(connection net.Conn) (EventListenerConnection, error) {
	// Send HTTP response to let the client know what to expect.
	// This is only sent once, and is followed by events.
	//
	// The X-Content-Type-Options response HTTP header is a marker used by the server to indicate
	// that the MIME types advertised in the Content-Type headers should be followed and not be
	// changed. The header allows you to avoid MIME type sniffing by saying that the MIME types are
	// deliberately configured.
	_, err := io.WriteString(connection, `HTTP/1.1 200 OK
Connection: keep-alive
Content-Type: application/json
X-Content-Type-Options: nosniff

`)
	if err != nil {
		return nil, fmt.Errorf("Failed sending initial HTTP response: %w", err)
	}

	return &streamListenerConnection{
		Conn: connection,
	}, nil
}

// Reader for the stream connection.
func (e *streamListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	readerCommon(ctx, &e.lock, e.Conn)
}

// WriteJSON sends a JSON event to the stream connection.
func (e *streamListenerConnection) WriteJSON(event any) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	err := e.SetWriteDeadline(time.Now().Add(5 * (time.Second)))
	if err != nil {
		return fmt.Errorf("Failed setting write deadline: %w", err)
	}

	err = json.NewEncoder(e.Conn).Encode(event)
	if err != nil {
		return fmt.Errorf("Failed sending event: %w", err)
	}

	return nil
}

// Close closes the stream connection.
func (e *streamListenerConnection) Close() error {
	return e.Conn.Close()
}

// NewSimpleListenerConnection returns a new simple listener connection.
func NewSimpleListenerConnection(rwc io.ReadWriteCloser) EventListenerConnection {
	return &simpleListenerConnection{
		rwc: rwc,
	}
}

// Reader for the simple connection.
func (e *simpleListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	readerCommon(ctx, &e.lock, e.rwc)
}

// WriteJSON sends a JSON event to the simple connection.
func (e *simpleListenerConnection) WriteJSON(event any) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	err := json.NewEncoder(e.rwc).Encode(event)
	if err != nil {
		return err
	}

	return nil
}

// Close closes the simple connection.
func (e *simpleListenerConnection) Close() error {
	return e.rwc.Close()
}

// LocalAddr returns nil for logging purposes.
func (e *simpleListenerConnection) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr returns nil for logging purposes.
func (e *simpleListenerConnection) RemoteAddr() net.Addr {
	return nil
}
