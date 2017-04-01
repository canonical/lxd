package lxd

import (
	"encoding/json"

	"github.com/lxc/lxd/shared"
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
	conn, err := r.websocket("/events")
	if err != nil {
		return nil, err
	}

	// Add the listener
	r.eventListeners = append(r.eventListeners, &listener)

	// And spawn the listener
	go func() {
		for {
			r.eventListenersLock.Lock()
			if len(r.eventListeners) == 0 {
				// We don't need the connection anymore, disconnect
				conn.Close()

				r.eventListeners = nil
				r.eventListenersLock.Unlock()
				break
			}
			r.eventListenersLock.Unlock()

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
				return
			}

			// Attempt to unpack the message
			message := make(map[string]interface{})
			err = json.Unmarshal(data, &message)
			if err != nil {
				continue
			}

			// Extract the message type
			_, ok := message["type"]
			if !ok {
				continue
			}
			messageType := message["type"].(string)

			// Send the message to all handlers
			r.eventListenersLock.Lock()
			for _, listener := range r.eventListeners {
				listener.targetsLock.Lock()
				for _, target := range listener.targets {
					if target.types != nil && !shared.StringInSlice(messageType, target.types) {
						continue
					}

					go target.function(message)
				}
				listener.targetsLock.Unlock()
			}
			r.eventListenersLock.Unlock()
		}
	}()

	return &listener, nil
}
