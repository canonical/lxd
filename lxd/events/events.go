package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pborman/uuid"

	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// Server represents an instance of an event server.
type Server struct {
	debug   bool
	verbose bool

	listeners map[string]*Listener
	lock      sync.Mutex
}

// NewServer returns a new event server.
func NewServer(debug bool, verbose bool) *Server {
	server := &Server{
		debug:     debug,
		verbose:   verbose,
		listeners: map[string]*Listener{},
	}

	return server
}

// AddListener creates and returns a new event listener.
func (s *Server) AddListener(group string, connection *websocket.Conn, messageTypes []string, location string, noForward bool) (*Listener, error) {
	ctx, ctxCancel := context.WithCancel(context.Background())

	listener := &Listener{
		Conn: connection,

		group:        group,
		messageTypes: messageTypes,
		location:     location,
		noForward:    noForward,
		ctx:          ctx,
		ctxCancel:    ctxCancel,
		id:           uuid.New(),
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.listeners[listener.id] != nil {
		return nil, fmt.Errorf("A listener with id '%s' already exists", listener.id)
	}

	s.listeners[listener.id] = listener

	go listener.heartbeat()

	return listener, nil
}

// SendLifecycle broadcasts a lifecycle event.
func (s *Server) SendLifecycle(group string, event api.EventLifecycle) {
	s.Send(group, "lifecycle", event)
}

// Send broadcasts a custom event.
func (s *Server) Send(group, eventType string, eventMessage interface{}) error {
	encodedMessage, err := json.Marshal(eventMessage)
	if err != nil {
		return err
	}
	event := api.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Metadata:  encodedMessage,
	}

	return s.broadcast(group, event, false)
}

// Forward to the local events dispatcher an event received from another node.
func (s *Server) Forward(id int64, event api.Event) {
	if event.Type == "logging" {
		// Parse the message
		logEntry := api.EventLogging{}
		err := json.Unmarshal(event.Metadata, &logEntry)
		if err != nil {
			return
		}

		if !s.debug && logEntry.Level == "dbug" {
			return
		}

		if !s.debug && !s.verbose && logEntry.Level == "info" {
			return
		}
	}

	err := s.broadcast("", event, true)
	if err != nil {
		logger.Warnf("Failed to forward event from node %d: %v", id, err)
	}
}

func (s *Server) broadcast(group string, event api.Event, isForward bool) error {
	s.lock.Lock()
	listeners := s.listeners
	for _, listener := range listeners {
		if group != "" && listener.group != "*" && group != listener.group {
			continue
		}

		if isForward && listener.noForward {
			continue
		}

		if !shared.StringInSlice(event.Type, listener.messageTypes) {
			continue
		}

		go func(listener *Listener, event api.Event) {
			// Check that the listener still exists
			if listener == nil {
				return
			}

			// Make sure we're not done already
			if listener.IsClosed() {
				return
			}

			// Set the Location to the expected serverName
			if event.Location == "" {
				eventCopy := api.Event{}
				err := shared.DeepCopy(&event, &eventCopy)
				if err != nil {
					return
				}
				eventCopy.Location = listener.location

				event = eventCopy
			}

			listener.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := listener.WriteJSON(event)
			if err != nil {
				// Remove the listener from the list
				s.lock.Lock()
				delete(s.listeners, listener.id)
				s.lock.Unlock()

				listener.Close()
			}
		}(listener, event)
	}
	s.lock.Unlock()

	return nil
}

// Listener describes an event listener.
type Listener struct {
	*websocket.Conn

	group        string
	messageTypes []string
	ctx          context.Context
	ctxCancel    func()
	id           string
	lock         sync.Mutex
	location     string
	lastPong     time.Time

	// If true, this listener won't get events forwarded from other
	// nodes. It only used by listeners created internally by LXD nodes
	// connecting to other LXD nodes to get their local events only.
	noForward bool
}

func (e *Listener) heartbeat() {
	pingInterval := time.Second * 5
	e.lastPong = time.Now() // To allow initial heartbeat ping to be sent.

	e.SetPongHandler(func(msg string) error {
		e.lastPong = time.Now()
		return nil
	})

	for {
		if e.IsClosed() {
			return
		}

		if e.lastPong.Add(pingInterval * 2).Before(time.Now()) {
			e.Close()
			return
		}

		e.lock.Lock()
		err := e.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(5*time.Second))
		if err == websocket.ErrCloseSent {
			e.lock.Unlock()
			return
		} else if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
			e.lock.Unlock()
			return
		} else if err != nil {
			e.lock.Unlock()
			e.Close()
			return
		}
		e.lock.Unlock()

		select {
		case <-time.After(pingInterval):
		case <-e.ctx.Done():
			return
		}
	}
}

// MessageTypes returns a list of message types the listener will be notified of.
func (e *Listener) MessageTypes() []string {
	return e.messageTypes
}

// IsClosed returns true if the listener is closed.
func (e *Listener) IsClosed() bool {
	return e.ctx.Err() != nil
}

// ID returns the listener ID.
func (e *Listener) ID() string {
	return e.id
}

// Wait waits for a message on its active channel or the context is cancelled, then returns.
func (e *Listener) Wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-e.ctx.Done():
	}
}

// Close Disconnects the listener.
func (e *Listener) Close() {
	if e.IsClosed() {
		return
	}

	logger.Debug("Disconnected event listener", log.Ctx{"listener": e.id})

	e.Conn.Close()
	e.ctxCancel()
}

// WriteJSON message to the connection.
func (e *Listener) WriteJSON(v interface{}) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	return e.Conn.WriteJSON(v)
}

// WriteMessage to the connection.
func (e *Listener) WriteMessage(messageType int, data []byte) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	return e.Conn.WriteMessage(messageType, data)
}
