package lxd

import (
	"github.com/gorilla/websocket"
)

// GetEvents connects to the devLXD event monitoring interface.
func (r *ProtocolDevLXD) GetEvents() (*EventListener, error) {
	// Wrap websocket connection in a function to allow the manager to
	// establish a new connection.
	getWebsocket := func() (*websocket.Conn, error) {
		return r.RawWebsocket("/events")
	}

	return r.eventListenerManager.getEvents(r.ctxConnected, getWebsocket, "")
}
