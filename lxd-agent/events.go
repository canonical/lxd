package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/grant-he/lxd/lxd/response"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
	"github.com/grant-he/lxd/shared/logger"
)

var eventsCmd = APIEndpoint{
	Path: "events",

	Get:  APIEndpointAction{Handler: eventsGet},
	Post: APIEndpointAction{Handler: eventsPost},
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
	typeStr := r.FormValue("type")
	if typeStr == "" {
		// We add 'config' here to allow listeners on /dev/lxd/sock to receive config changes.
		typeStr = "logging,operation,lifecycle,config"
	}

	// Upgrade the connection to websocket
	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer c.Close() // This ensures the go routine below is ended when this function ends.

	// If this request is an internal one initiated by another node wanting
	// to watch the events on this node, set the listener to broadcast only
	// local events.
	listener, err := d.events.AddListener("default", c, strings.Split(typeStr, ","), "lxd-agent", false)
	if err != nil {
		return err
	}

	logger.Debugf("New event listener: %s (%v)", listener.ID(), typeStr)

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
		defer logger.Debugf("Event listener client disconnected: %s", listener.ID())
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

func eventsPost(d *Daemon, r *http.Request) response.Response {
	var event api.Event

	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		return response.InternalError(err)
	}

	err = d.events.Send("", event.Type, event.Metadata)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, nil)
}
