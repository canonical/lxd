package lxd

import (
	"errors"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
)

// getEvents connects to the LXD monitoring interface.
func (r *ProtocolLXD) getEvents(allProjects bool) (*EventListener, error) {
	// Resolve the project name.
	connInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	if connInfo.Project == "" {
		return nil, errors.New("Unexpected empty project in connection info")
	}

	project := ""
	if !allProjects {
		project = connInfo.Project
	}

	// Wrap websocket connection in a function to allow the manager to
	// establish a new connection.
	getWebsocket := func() (*websocket.Conn, error) {
		// Resolve LXD events URL.
		var url string
		var err error
		if allProjects {
			url, err = r.setQueryAttributes("/events?all-projects=true")
		} else {
			url, err = r.setQueryAttributes("/events")
		}

		if err != nil {
			return nil, err
		}

		return r.websocket(url)
	}

	return r.eventListenerManager.getEvents(r.ctxConnected, getWebsocket, project)
}

// GetEvents gets the events for the project defined on the client.
func (r *ProtocolLXD) GetEvents() (*EventListener, error) {
	return r.getEvents(false)
}

// GetEventsAllProjects gets events for all projects.
func (r *ProtocolLXD) GetEventsAllProjects() (*EventListener, error) {
	return r.getEvents(true)
}

// SendEvent send an event to the server via the client's event listener connection.
func (r *ProtocolLXD) SendEvent(event api.Event) error {
	return r.eventListenerManager.SendEvent(event)
}
