package lxd

import (
	"fmt"
	"sync"
)

// The EventListener struct is used to interact with a LXD event stream
type EventListener struct {
	r            *ProtocolLXD
	chActive     chan bool
	disconnected bool
	err          error

	targets     []*EventTarget
	targetsLock sync.Mutex
}

// The EventTarget struct is returned to the caller of AddHandler and used in RemoveHandler
type EventTarget struct {
	function func(interface{})
	types    []string
}

// AddHandler adds a function to be called whenever an event is received
func (e *EventListener) AddHandler(types []string, function func(interface{})) (*EventTarget, error) {
	if function == nil {
		return nil, fmt.Errorf("A valid function must be provided")
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

// RemoveHandler removes a function to be called whenever an event is received
func (e *EventListener) RemoveHandler(target *EventTarget) error {
	if target == nil {
		return fmt.Errorf("A valid event target must be provided")
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

	return fmt.Errorf("Couldn't find this function and event types combination")
}

// Disconnect must be used once done listening for events
func (e *EventListener) Disconnect() {
	if e.disconnected {
		return
	}

	// Handle locking
	e.r.eventListenersLock.Lock()
	defer e.r.eventListenersLock.Unlock()

	// Locate and remove it from the global list
	for i, listener := range e.r.eventListeners {
		if listener == e {
			copy(e.r.eventListeners[i:], e.r.eventListeners[i+1:])
			e.r.eventListeners[len(e.r.eventListeners)-1] = nil
			e.r.eventListeners = e.r.eventListeners[:len(e.r.eventListeners)-1]
			break
		}
	}

	// Turn off the handler
	e.err = nil
	e.disconnected = true
	close(e.chActive)
}

// Wait hangs until the server disconnects the connection or Disconnect() is called
func (e *EventListener) Wait() error {
	<-e.chActive
	return e.err
}

// IsActive returns true if this listener is still connected, false otherwise.
func (e *EventListener) IsActive() bool {
	select {
	case <-e.chActive:
		return false // If the chActive channel is closed we got disconnected
	default:
		return true
	}
}
