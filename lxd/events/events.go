package events

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pborman/uuid"

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
func (s *Server) AddListener(group string, connection *websocket.Conn, messageTypes []string, location string, noForward bool) (*Listener, error) {
	listener := &Listener{
		group:        group,
		connection:   connection,
		messageTypes: messageTypes,
		location:     location,
		noForward:    noForward,
		active:       make(chan bool, 1),
		id:           uuid.NewRandom().String(),
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.listeners[listener.id] != nil {
		return nil, fmt.Errorf("A listener with id '%s' already exists", listener.id)
	}

	s.listeners[listener.id] = listener

	return listener, nil
}

// SendLifecycle broadcasts a lifecycle event.
func (s *Server) SendLifecycle(group, action, source string,
	context map[string]interface{}) error {
	s.Send(group, "lifecycle", api.EventLifecycle{
		Action:  action,
		Source:  source,
		Context: context})
	return nil
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

			// Ensure there is only a single even going out at the time
			listener.lock.Lock()
			defer listener.lock.Unlock()

			// Make sure we're not done already
			if listener.done {
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

			body, err := json.Marshal(event)
			if err != nil {
				return
			}

			err = listener.connection.WriteMessage(websocket.TextMessage, body)
			if err != nil {
				// Remove the listener from the list
				s.lock.Lock()
				delete(s.listeners, listener.id)
				s.lock.Unlock()

				// Disconnect the listener
				listener.connection.Close()
				listener.active <- false
				listener.done = true
				logger.Debugf("Disconnected event listener: %s", listener.id)
			}
		}(listener, event)
	}
	s.lock.Unlock()

	return nil
}

// Listener describes an event listener.
type Listener struct {
	group        string
	connection   *websocket.Conn
	messageTypes []string
	active       chan bool
	id           string
	lock         sync.Mutex
	done         bool
	location     string

	// If true, this listener won't get events forwarded from other
	// nodes. It only used by listeners created internally by LXD nodes
	// connecting to other LXD nodes to get their local events only.
	noForward bool
}

// MessageTypes returns a list of message types the listener will be notified of.
func (e *Listener) MessageTypes() []string {
	return e.messageTypes
}

// IsDone returns true if the listener is done.
func (e *Listener) IsDone() bool {
	return e.done
}

// Connection returns the underlying websocket connection.
func (e *Listener) Connection() *websocket.Conn {
	return e.connection
}

// ID returns the listener ID.
func (e *Listener) ID() string {
	return e.id
}

// Wait waits for a message on its active channel, then returns.
func (e *Listener) Wait() {
	<-e.active
}

// Lock locks the internal mutex.
func (e *Listener) Lock() {
	e.lock.Lock()
}

// Unlock unlocks the internal mutex.
func (e *Listener) Unlock() {
	e.lock.Unlock()
}

// Deactivate deactivates the event listener.
func (e *Listener) Deactivate() {
	e.active <- false
	e.done = true
}
