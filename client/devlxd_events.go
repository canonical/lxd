package lxd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// DevLXDEventListener is a wrapper around the EventListener struct
// that is used to interact with the devLXD event stream.
type DevLXDEventListener struct {
	EventListener
	r *ProtocolDevLXD
}

// Disconnect must be used once done listening for events.
func (e *DevLXDEventListener) Disconnect() {
	// Handle locking
	e.r.eventListenersLock.Lock()
	defer e.r.eventListenersLock.Unlock()

	if e.ctx.Err() != nil {
		return
	}

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
	e.ctxCancel()
}

// GetEvents connects to the devLXD event monitoring interface.
func (r *ProtocolDevLXD) GetEvents() (*DevLXDEventListener, error) {
	// Prevent anything else from interacting with the listeners
	r.eventListenersLock.Lock()
	defer r.eventListenersLock.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	// Setup a new listener
	listener := DevLXDEventListener{
		r: r,
		EventListener: EventListener{
			ctx:       ctx,
			ctxCancel: cancel,
		},
	}

	// There is an existing Go routine for the required project filter, so just add another target.
	if len(r.eventListeners) > 0 {
		r.eventListeners = append(r.eventListeners, &listener)
		return &listener, nil
	}

	// Setup a new connection with devLXD using a websocket.
	wsConn, err := r.RawWebsocket("/events")
	if err != nil {
		return nil, err
	}

	r.eventConnLock.Lock()
	r.eventConn = wsConn // Save for others to use.
	r.eventConnLock.Unlock()

	// Initialize the event listener list if we were able to connect to the events websocket.
	r.eventListeners = []*DevLXDEventListener{&listener}

	// Spawn a watcher that will close the websocket connection after all
	// listeners are gone.
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-time.After(time.Minute):
			case <-r.ctx.Done():
			case <-stopCh:
			}

			r.eventListenersLock.Lock()
			r.eventConnLock.Lock()
			if len(r.eventListeners) == 0 {
				// We don't need the connection anymore, disconnect and clear.
				if r.eventConn != nil {
					_ = r.eventConn.Close()
					r.eventConn = nil
				}

				r.eventListenersLock.Unlock()
				r.eventConnLock.Unlock()
				return
			}

			r.eventListenersLock.Unlock()
			r.eventConnLock.Unlock()
		}
	}()

	// Spawn the listener
	go func() {
		for {
			_, data, err := wsConn.ReadMessage()
			if err != nil {
				// Prevent anything else from interacting with the listeners
				r.eventListenersLock.Lock()
				defer r.eventListenersLock.Unlock()

				// Tell all the current listeners about the failure
				for _, listener := range r.eventListeners {
					listener.err = err
					listener.ctxCancel()
				}

				// And remove them all from the list so that when watcher routine runs it will
				// close the websocket connection.
				r.eventListeners = nil

				close(stopCh) // Instruct watcher go routine to cleanup.
				return
			}

			// Attempt to unmarshal the message into an event.
			event := api.Event{}
			err = json.Unmarshal(data, &event)
			if err != nil {
				fmt.Println(">> [SKIP EVENT] Failed to unmarshal event:", err)
				continue
			}

			// Skip events without a message type.
			if event.Type == "" {
				fmt.Println(">> [SKIP EVENT] Event type is empty")
				continue
			}

			// Send the message to all handlers
			r.eventListenersLock.Lock()
			for _, listener := range r.eventListeners {
				listener.targetsLock.Lock()
				for _, target := range listener.targets {
					if target.types != nil && !shared.ValueInSlice(event.Type, target.types) {
						continue
					}

					go target.function(event)
				}

				listener.targetsLock.Unlock()
			}

			r.eventListenersLock.Unlock()
		}
	}()

	return &listener, nil
}
