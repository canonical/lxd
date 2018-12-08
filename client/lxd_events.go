package lxd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Event handling functions

// GetEvents connects to the LXD monitoring interface
func (r *ProtocolLXD) GetEvents() (*EventListener, error) {
	// Prevent anything else from interacting with the listeners
	r.eventListenersLock.Lock()
	defer r.eventListenersLock.Unlock()

	// Setup a new listener
	listener := EventListener{
		r:        r,
		chActive: make(chan bool),
	}

	if r.eventListeners != nil {
		// There is an existing Go routine setup, so just add another target
		r.eventListeners = append(r.eventListeners, &listener)
		return &listener, nil
	}

	// Initialize the list if needed
	r.eventListeners = []*EventListener{}

	// Setup a new connection with LXD
	conn, err := r.websocket(fmt.Sprintf("/events?project=%s", r.project))
	if err != nil {
		return nil, err
	}

	// Add the listener
	r.eventListeners = append(r.eventListeners, &listener)

	// Spawn a watcher that will close the websocket connection after all
	// listeners are gone.
	stopCh := make(chan struct{}, 0)
	go func() {
		for {
			select {
			case <-time.After(time.Minute):
			case <-stopCh:
				break
			}

			r.eventListenersLock.Lock()
			if len(r.eventListeners) == 0 {
				// We don't need the connection anymore, disconnect
				conn.Close()

				r.eventListeners = nil
				r.eventListenersLock.Unlock()
				break
			}
			r.eventListenersLock.Unlock()
		}
	}()

	// Spawn the listener
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				// Prevent anything else from interacting with the listeners
				r.eventListenersLock.Lock()
				defer r.eventListenersLock.Unlock()

				// Tell all the current listeners about the failure
				for _, listener := range r.eventListeners {
					listener.err = err
					listener.disconnected = true
					close(listener.chActive)
				}

				// And remove them all from the list
				r.eventListeners = []*EventListener{}

				conn.Close()
				close(stopCh)

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
			r.eventListenersLock.Lock()
			for _, listener := range r.eventListeners {
				listener.targetsLock.Lock()
				for _, target := range listener.targets {
					if target.types != nil && !shared.StringInSlice(event.Type, target.types) {
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
