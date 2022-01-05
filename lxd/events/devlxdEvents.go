package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pborman/uuid"

	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// DevLXDServer represents an instance of an devlxd event server.
type DevLXDServer struct {
	serverCommon

	listeners map[string]*DevLXDListener
}

// NewDevLXDServer returns a new devlxd event server.
func NewDevLXDServer(debug bool, verbose bool) *DevLXDServer {
	server := &DevLXDServer{
		serverCommon: serverCommon{
			debug:   debug,
			verbose: verbose,
		},
		listeners: map[string]*DevLXDListener{},
	}

	return server
}

// AddListener creates and returns a new event listener.
func (s *DevLXDServer) AddListener(projectName string, allProjects bool, connection *websocket.Conn, messageTypes []string, location string, localOnly bool) (*DevLXDListener, error) {
	if allProjects && projectName != "" {
		return nil, fmt.Errorf("Cannot specify project name when listening for events on all projects")
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	listener := &DevLXDListener{
		listenerCommon: listenerCommon{
			Conn:         connection,
			messageTypes: messageTypes,
			location:     location,
			localOnly:    localOnly,
			ctx:          ctx,
			ctxCancel:    ctxCancel,
			id:           uuid.New(),
		},
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

// Send broadcasts a custom event.
func (s *DevLXDServer) Send(projectName string, eventType string, eventMessage interface{}) error {
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

func (s *DevLXDServer) broadcast(event api.Event, isForward bool) error {
	s.lock.Lock()
	listeners := s.listeners
	for _, listener := range listeners {
		if isForward && listener.localOnly {
			continue
		}

		if !shared.StringInSlice(event.Type, listener.messageTypes) {
			continue
		}

		go func(listener *DevLXDListener, event api.Event) {
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

// DevLXDListener describes a devlxd event listener.
type DevLXDListener struct {
	listenerCommon
}
