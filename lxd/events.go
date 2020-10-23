package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

var eventTypes = []string{"logging", "operation", "lifecycle"}
var privilegedEventTypes = []string{"logging"}

var eventsCmd = APIEndpoint{
	Path: "events",

	Get: APIEndpointAction{Handler: eventsGet, AccessHandler: allowAuthenticated},
}

type eventsServe struct {
	req *http.Request
	d   *Daemon
}

func (r *eventsServe) Render(w http.ResponseWriter) error {
	return eventsSocket(r.d, r.req, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(d *Daemon, r *http.Request, w http.ResponseWriter) error {
	project := projectParam(r)
	types := strings.Split(r.FormValue("type"), ",")
	if len(types) == 1 && types[0] == "" {
		types = []string{}
		for _, entry := range eventTypes {
			if !d.userIsAdmin(r) && shared.StringInSlice(entry, privilegedEventTypes) {
				continue
			}

			types = append(types, entry)
		}
	}

	// Validate event types.
	for _, entry := range types {
		if !shared.StringInSlice(entry, eventTypes) {
			response.BadRequest(fmt.Errorf("'%s' isn't a supported event type", entry)).Render(w)
			return nil
		}
	}

	if shared.StringInSlice("logging", types) && !d.userIsAdmin(r) {
		response.Forbidden(nil).Render(w)
		return nil
	}

	// Upgrade the connection to websocket
	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer c.Close() // This ensures the go routine below is ended when this function ends.

	// Get the current local serverName and store it for the events
	// We do that now to avoid issues with changes to the name and to limit
	// the number of DB access to just one per connection
	var serverName string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		serverName, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return err
	}

	// If this request is an internal one initiated by another node wanting
	// to watch the events on this node, set the listener to broadcast only
	// local events.
	listener, err := d.events.AddListener(project, c, types, serverName, isClusterNotification(r))
	if err != nil {
		return err
	}

	logger.Debugf("New event listener: %s", listener.ID())

	// Create a cancellable context from the request context. Once the request has been upgraded
	// to a websocket the request's context doesn't appear to be cancelled when the client
	// disconnects (even though its documented as such). But we wrap the request's context here
	// anyway just in case its fixed in the future.
	ctx, cancel := context.WithCancel(r.Context())

	// Instead of relying on the request's context to be cancelled when the client connection
	// is closed (see above), we instead enter into a repeat read loop of the connection in
	// order to detect when the client connection is closed. This should be fine as for the
	// events route there is no expectation to read any useful data from the client.
	go func() {
		for {
			_, _, err := c.NextReader()
			if err != nil {
				// Client read error (likely premature close), so cancel context.
				cancel()
				return
			}
		}
	}()

	listener.Wait(ctx)
	logger.Debugf("Event listener finished: %s", listener.ID())

	return nil
}

func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{req: r, d: d}
}
