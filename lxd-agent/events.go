package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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

	// As we don't know which project we are in, subscribe to events from all projects.
	listener, err := d.events.AddListener("", true, c, strings.Split(typeStr, ","), nil, nil, nil)
	if err != nil {
		return err
	}

	listener.Wait(r.Context())

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
