package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/ws"
)

var eventsCmd = APIEndpoint{
	Path: "events",

	Get:  APIEndpointAction{Handler: eventsGet},
	Post: APIEndpointAction{Handler: eventsPost},
}

type eventsServe struct {
	d *Daemon
}

// Render starts event socket.
func (r *eventsServe) Render(w http.ResponseWriter, request *http.Request) error {
	return eventsSocket(r.d, request, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(d *Daemon, r *http.Request, w http.ResponseWriter) error {
	typeStr := r.FormValue("type")
	if typeStr == "" {
		// We add 'config' here to allow listeners on /dev/lxd/sock to receive config changes.
		typeStr = "logging,operation,lifecycle,config,device"
	}

	var listenerConnection events.EventListenerConnection

	// If the client has not requested a websocket connection then fallback to long polling event stream mode.
	if r.Header.Get("Upgrade") == "websocket" {
		// Upgrade the connection to websocket
		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return err
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection = events.NewWebsocketListenerConnection(conn)
	} else {
		h, ok := w.(http.Hijacker)
		if !ok {
			return fmt.Errorf("Missing implemented http.Hijacker interface")
		}

		conn, _, err := h.Hijack()
		if err != nil {
			return err
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection, err = events.NewStreamListenerConnection(conn)
		if err != nil {
			return err
		}
	}

	// As we don't know which project we are in, subscribe to events from all projects.
	listener, err := d.events.AddListener("", true, nil, listenerConnection, strings.Split(typeStr, ","), nil, nil, nil)
	if err != nil {
		return err
	}

	listener.Wait(r.Context())

	return nil
}

func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{d: d}
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

	// Handle device related actions locally.
	go eventsProcess(event)

	return response.SyncResponse(true, nil)
}

func eventsProcess(event api.Event) {
	// We currently only need to react to device events.
	if event.Type != "device" {
		return
	}

	type deviceEvent struct {
		Action string                    `json:"action"`
		Config map[string]string         `json:"config"`
		Name   string                    `json:"name"`
		Mount  instancetype.VMAgentMount `json:"mount"`
	}

	e := deviceEvent{}
	err := json.Unmarshal(event.Metadata, &e)
	if err != nil {
		return
	}

	// Only care about device additions, we don't try to handle remove.
	if e.Action != "added" {
		return
	}

	// We only handle disk hotplug.
	if e.Config["type"] != "disk" {
		return
	}

	// And only for path based devices.
	if e.Config["path"] == "" {
		return
	}

	// Attempt to perform the mount.
	mntSource := fmt.Sprintf("lxd_%s", e.Name)
	if e.Mount.Source != "" {
		mntSource = e.Mount.Source
	}

	l := logger.AddContext(logger.Ctx{"type": "virtiofs", "source": mntSource, "path": e.Config["path"]})

	_ = os.MkdirAll(e.Config["path"], 0755)

	for i := 0; i < 5; i++ {
		_, err = shared.RunCommand("mount", "-t", "virtiofs", mntSource, e.Config["path"])
		if err == nil {
			l.Info("Mounted hotplug")
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	l.Info("Failed to mount hotplug", logger.Ctx{"err": err})
}
