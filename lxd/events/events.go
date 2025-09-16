package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/logger"
)

// EventSource indicates the source of an event.
type EventSource uint8

// EventSourceLocal indicates the event was generated locally.
const EventSourceLocal = 0

// EventSourcePull indicates the event was received from an outbound event listener stream.
const EventSourcePull = 1

// EventSourcePush indicates the event was received from an event listener client connected to us.
const EventSourcePush = 2

// InjectFunc is used to inject an event received by a listener into the local events dispatcher.
type InjectFunc func(event api.Event, eventSource EventSource)

// NotifyFunc is called when an event is dispatched.
type NotifyFunc func(event api.Event)

// Server represents an instance of an event server.
type Server struct {
	serverCommon

	listeners map[string]*Listener
	notify    NotifyFunc
	location  string

	// logger is a [logger.Logger] that can be used while an event is being processed.
	// This is necessary because the global [logger.Logger] is configured with a hook that will send logging events to the server.
	// If the global logger is used while the Server is locked, the Server goes into deadlock. This logger should be used instead.
	logger logger.Logger
}

// NewServer returns a new event server.
func NewServer(debug bool, verbose bool, notify NotifyFunc) (*Server, error) {
	eventServerLogger, err := logger.New("", "", verbose, debug, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to instantiate event server logger: %w", err)
	}

	server := &Server{
		serverCommon: serverCommon{
			debug:   debug,
			verbose: verbose,
		},
		listeners: map[string]*Listener{},
		notify:    notify,
		logger:    eventServerLogger,
	}

	return server, nil
}

// SetLocalLocation sets the local location of this member.
// This value will be added to the Location event field if not populated from another member.
func (s *Server) SetLocalLocation(location string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.location = location
}

// AddListener creates and returns a new event listener. The filter argument must return true to include the event and
// false to omit the event.
//
// Warn: The filter must not call the default logger or send any events of its own. Otherwise, the event server will
// deadlock when it tries to broadcast the logging event.
func (s *Server) AddListener(projectName string, allProjects bool, filter func(logger.Logger, api.Event) bool, connection EventListenerConnection, messageTypes []string, excludeSources []EventSource, recvFunc EventHandler, excludeLocations []string) (*Listener, error) {
	if allProjects && projectName != "" {
		return nil, errors.New("Cannot specify project name when listening for events on all projects")
	}

	if filter == nil {
		filter = func(logger.Logger, api.Event) bool {
			return true
		}
	}

	listener := &Listener{
		listenerCommon: listenerCommon{
			EventListenerConnection: connection,
			messageTypes:            messageTypes,
			done:                    cancel.New(),
			id:                      uuid.New().String(),
			recvFunc:                recvFunc,
		},

		allProjects:      allProjects,
		projectName:      projectName,
		filter:           filter,
		excludeSources:   excludeSources,
		excludeLocations: excludeLocations,
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.listeners[listener.id] != nil {
		return nil, fmt.Errorf("A listener with ID %q already exists", listener.id)
	}

	s.listeners[listener.id] = listener

	go listener.start()

	return listener, nil
}

// SendLifecycle broadcasts a lifecycle event.
func (s *Server) SendLifecycle(projectName string, event api.EventLifecycle) {
	_ = s.Send(projectName, api.EventTypeLifecycle, event)
}

// Send broadcasts a custom event.
func (s *Server) Send(projectName string, eventType string, eventMessage any) error {
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

	return s.broadcast(event, EventSourceLocal)
}

// Inject an event from another member into the local events dispatcher.
// eventSource is used to indicate where this event was received from.
func (s *Server) Inject(event api.Event, eventSource EventSource) {
	if event.Type == api.EventTypeLogging {
		// Parse the message
		logEntry := api.EventLogging{}
		err := json.Unmarshal(event.Metadata, &logEntry)
		if err != nil {
			return
		}

		if !s.debug && logEntry.Level == "debug" {
			return
		}

		if !s.debug && !s.verbose && logEntry.Level == "info" {
			return
		}
	}

	err := s.broadcast(event, eventSource)
	if err != nil {
		logger.Warn("Failed to forward event from member", logger.Ctx{"member": event.Location, "err": err})
	}
}

func (s *Server) broadcast(event api.Event, eventSource EventSource) error {
	sourceInSlice := func(source EventSource, sources []EventSource) bool {
		return slices.Contains(sources, source)
	}

	s.lock.Lock()

	// Set the Location for local events to the local serverName if not already populated (do it here rather
	// than in Send as the lock to read s.location has been taken here already).
	if eventSource == EventSourceLocal && event.Location == "" {
		event.Location = s.location
	}

	// If a notifcation hook is present, then call it for locally produced events.
	// This can be used to send local events to another target (such as an event-hub member).
	if s.notify != nil && eventSource == EventSourceLocal {
		s.notify(event)
	}

	filterLogger := s.logger.AddContext(logger.Ctx{"source": eventSource})
	listeners := s.listeners
	for _, listener := range listeners {
		// If the event is project specific, check if the listener is requesting events from that project.
		if event.Project != "" && !listener.allProjects && event.Project != listener.projectName {
			continue
		}

		if sourceInSlice(eventSource, listener.excludeSources) {
			continue
		}

		if !slices.Contains(listener.messageTypes, event.Type) {
			continue
		}

		// If the event doesn't come from this member and has been excluded by listener, don't deliver it.
		if eventSource != EventSourceLocal && slices.Contains(listener.excludeLocations, event.Location) {
			continue
		}

		// Apply any further filters.
		if !listener.filter(filterLogger, event) {
			continue
		}

		go func(listener *Listener, event api.Event) {
			// Check that the listener still exists
			if listener == nil {
				return
			}

			// Make sure we're not done already
			if listener.IsClosed() {
				// Remove the listener from the list
				s.lock.Lock()
				delete(s.listeners, listener.id)
				s.lock.Unlock()
				return
			}

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
	listenerCommon

	allProjects      bool
	projectName      string
	filter           func(logger.Logger, api.Event) bool
	excludeSources   []EventSource
	excludeLocations []string
}
