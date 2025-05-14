package lxd

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
)

// The EventListener struct is used to interact with a LXD event stream.
type EventListener struct {
	ctx       context.Context
	ctxCancel context.CancelFunc
	err       error

	// projectName stores which project this event listener is associated with (empty for all projects).
	projectName string
	targets     []*EventTarget
	targetsLock sync.Mutex
}

// The EventTarget struct is returned to the caller of AddHandler and used in RemoveHandler.
type EventTarget struct {
	function func(api.Event)
	types    []string
}

// AddHandler adds a function to be called whenever an event is received.
func (e *EventListener) AddHandler(types []string, function func(api.Event)) (*EventTarget, error) {
	if function == nil {
		return nil, errors.New("A valid function must be provided")
	}

	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// Create a new target
	target := EventTarget{
		function: function,
		types:    types,
	}

	// And add it to the targets
	e.targets = append(e.targets, &target)

	return &target, nil
}

// RemoveHandler removes a function to be called whenever an event is received.
func (e *EventListener) RemoveHandler(target *EventTarget) error {
	if target == nil {
		return errors.New("A valid event target must be provided")
	}

	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// Locate and remove the function from the list
	for i, entry := range e.targets {
		if entry == target {
			copy(e.targets[i:], e.targets[i+1:])
			e.targets[len(e.targets)-1] = nil
			e.targets = e.targets[:len(e.targets)-1]
			return nil
		}
	}

	return errors.New("Couldn't find this function and event types combination")
}

// Disconnect must be used once done listening for events.
func (e *EventListener) Disconnect() {
	// Turn off the handler
	e.err = nil
	e.ctxCancel()
}

// Wait blocks until the server disconnects the connection or Disconnect() is called.
func (e *EventListener) Wait() error {
	<-e.ctx.Done()
	return e.err
}

// IsActive returns true if this listener is still connected, false otherwise.
func (e *EventListener) IsActive() bool {
	return e.ctx.Err() == nil
}

// The eventListenerManager is used to manage event listeners.
type eventListenerManager struct {
	ctx context.Context

	// eventConns contains event listener connections associated to a project name (or empty for all projects).
	eventConns map[string]*websocket.Conn

	// eventConnsLock controls write access to the eventConns.
	eventConnsLock sync.Mutex

	// eventListeners is a slice of event listeners associated to a project name (or empty for all projects).
	eventListeners     map[string][]*EventListener
	eventListenersLock sync.Mutex
}

// newEventListenerManager creates a new event listener manager.
func newEventListenerManager(ctx context.Context) *eventListenerManager {
	return &eventListenerManager{
		ctx:            ctx,
		eventConns:     make(map[string]*websocket.Conn),
		eventListeners: make(map[string][]*EventListener),
	}
}

// getEvents connects to the LXD monitoring interface.
func (m *eventListenerManager) getEvents(ctxConnected context.Context, websocketHook func() (*websocket.Conn, error), project string) (*EventListener, error) {
	// If a specific project is not provided, listen to all projects.
	allProjects := project == ""

	// Prevent anything else from interacting with the listeners
	m.eventListenersLock.Lock()
	defer m.eventListenersLock.Unlock()

	revert := revert.New()
	defer revert.Fail()

	ctx, cancel := context.WithCancel(context.Background())

	// Make sure the context is cancelled in case of an error
	// to prevent leaking a go routine that waits for it.
	revert.Add(func() {
		cancel()
	})

	// Setup a new listener
	listener := EventListener{
		ctx:       ctx,
		ctxCancel: cancel,
	}

	if !allProjects {
		listener.projectName = project
	}

	// Make sure we remove the listener from the list when it's context is done.
	go func() {
		<-ctx.Done()
		m.eventListenersLock.Lock()
		defer m.eventListenersLock.Unlock()

		// Locate and remove the listener from the global list
		for i, l := range m.eventListeners[listener.projectName] {
			if &listener == l {
				copy(m.eventListeners[listener.projectName][i:], m.eventListeners[listener.projectName][i+1:])
				m.eventListeners[listener.projectName][len(m.eventListeners[listener.projectName])-1] = nil
				m.eventListeners[listener.projectName] = m.eventListeners[listener.projectName][:len(m.eventListeners[listener.projectName])-1]
				return
			}
		}
	}()

	// There is an existing Go routine for the required project filter, so just add another target.
	if m.eventListeners[listener.projectName] != nil {
		m.eventListeners[listener.projectName] = append(m.eventListeners[listener.projectName], &listener)
		revert.Success()
		return &listener, nil
	}

	// Connect to the websocket.
	wsConn, err := websocketHook()
	if err != nil {
		return nil, err
	}

	m.eventConnsLock.Lock()
	m.eventConns[listener.projectName] = wsConn // Save for others to use.
	m.eventConnsLock.Unlock()

	// Initialize the event listener list if we were able to connect to the events websocket.
	m.eventListeners[listener.projectName] = []*EventListener{&listener}

	// Spawn a watcher that will close the websocket connection after all
	// listeners are gone.
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-time.After(time.Minute):
			case <-ctxConnected.Done():
			case <-stopCh:
			}

			m.eventListenersLock.Lock()
			m.eventConnsLock.Lock()
			if len(m.eventListeners[listener.projectName]) == 0 {
				// We don't need the connection anymore, disconnect and clear.
				if m.eventListeners[listener.projectName] != nil {
					_ = m.eventConns[listener.projectName].Close()
					delete(m.eventConns, listener.projectName)
				}

				m.eventListeners[listener.projectName] = nil
				m.eventListenersLock.Unlock()
				m.eventConnsLock.Unlock()

				return
			}

			m.eventListenersLock.Unlock()
			m.eventConnsLock.Unlock()
		}
	}()

	// Spawn the listener
	go func() {
		for {
			_, data, err := wsConn.ReadMessage()
			if err != nil {
				// Prevent anything else from interacting with the listeners
				m.eventListenersLock.Lock()
				defer m.eventListenersLock.Unlock()

				// Tell all the current listeners about the failure
				for _, listener := range m.eventListeners[listener.projectName] {
					listener.err = err
					listener.ctxCancel()
				}

				// And remove them all from the list so that when watcher routine runs it will
				// close the websocket connection.
				m.eventListeners[listener.projectName] = nil

				close(stopCh) // Instruct watcher go routine to cleanup.
				return
			}

			// Attempt to unpack the message
			event := api.Event{}
			err = json.Unmarshal(data, &event)
			if err != nil {
				continue
			}

			// Extract the message type
			if event.Type == "" {
				continue
			}

			// Send the message to all handlers
			m.eventListenersLock.Lock()
			for _, listener := range m.eventListeners[listener.projectName] {
				listener.targetsLock.Lock()
				for _, target := range listener.targets {
					if target.types != nil && !slices.Contains(target.types, event.Type) {
						continue
					}

					go target.function(event)
				}

				listener.targetsLock.Unlock()
			}

			m.eventListenersLock.Unlock()
		}
	}()

	revert.Success()
	return &listener, nil
}

// SendEvent send an event to the server via the client's event listener connection.
func (m *eventListenerManager) SendEvent(event api.Event) error {
	m.eventConnsLock.Lock()
	defer m.eventConnsLock.Unlock()

	// Find an available event listener connection.
	// It doesn't matter which project the event listener connection is using, as this only affects which
	// events are received from the server, not which events we can send to it.
	var eventConn *websocket.Conn
	for _, eventConn = range m.eventConns {
		break
	}

	if eventConn == nil {
		return errors.New("No available event listener connection")
	}

	deadline, ok := m.ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}

	_ = eventConn.SetWriteDeadline(deadline)
	return eventConn.WriteJSON(event)
}
