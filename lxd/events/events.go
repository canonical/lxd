package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pborman/uuid"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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
func (s *Server) AddListener(projectName string, allProjects bool, connection *websocket.Conn, messageTypes []string, location string, localOnly bool) (*Listener, error) {
	if allProjects && projectName != "" {
		return nil, fmt.Errorf("Cannot specify project name when listening for events on all projects")
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	listener := &Listener{
		Conn: connection,

		allProjects:  allProjects,
		projectName:  projectName,
		messageTypes: messageTypes,
		location:     location,
		localOnly:    localOnly,
		ctx:          ctx,
		ctxCancel:    ctxCancel,
		id:           uuid.New(),
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.listeners[listener.id] != nil {
		return nil, fmt.Errorf("A listener with ID %q already exists", listener.id)
	}

	s.listeners[listener.id] = listener

	go listener.heartbeat()

	return listener, nil
}

// SendLifecycle broadcasts a lifecycle event.
func (s *Server) SendLifecycle(projectName string, event api.EventLifecycle) {
	s.Send(projectName, "lifecycle", event)
}

// Send broadcasts a custom event.
func (s *Server) Send(projectName string, eventType string, eventMessage interface{}) error {
	encodedMessage, err := json.Marshal(eventMessage)
	if err != nil {
		return err
	}
	event := api.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Metadata:  encodedMessage,
		Project:   projectName,
	}

	return s.broadcast(event, false)
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

	err := s.broadcast(event, true)
	if err != nil {
		logger.Warnf("Failed to forward event from member %d: %v", id, err)
	}
}

func (s *Server) broadcast(event api.Event, isForward bool) error {
	s.lock.Lock()
	listeners := s.listeners
	for _, listener := range listeners {
		// If the event is project specific, check if the listener is requesting events from that project.
		if event.Project != "" && !listener.allProjects && event.Project != listener.projectName {
			continue
		}

		if isForward && listener.localOnly {
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

	allProjects  bool
	projectName  string
	messageTypes []string
	ctx          context.Context
	ctxCancel    func()
	id           string
	lock         sync.Mutex
	location     string
	pongsPending uint

	// If true, this listener won't get events forwarded from other
	// nodes. It only used by listeners created internally by LXD nodes
	// connecting to other LXD nodes to get their local events only.
	localOnly bool
}

func (e *Listener) heartbeat() {
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
