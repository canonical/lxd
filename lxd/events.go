package main

import (
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

var eventsCmd = APIEndpoint{
	Path: "events",

	Get: APIEndpointAction{Handler: eventsGet, AccessHandler: AllowAuthenticated},
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
	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "logging,operation,lifecycle"
	}

	// Upgrade the connection to websocket
	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	// Get the current local serverName and store it for the events
	// We do that now to avoid issues with changes to the name and to limit
	// the number of DB access to just one per connection
	var serverName string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		serverName, err = tx.NodeName()
		return err
	})
	if err != nil {
		return err
	}

	// If this request is an internal one initiated by another node wanting
	// to watch the events on this node, set the listener to broadcast only
	// local events.
	listener := events.NewEventListener(project, c, strings.Split(typeStr, ","), serverName, isClusterNotification(r))

	events.AddListener(listener)

	logger.Debugf("New event listener: %s", listener.ID())

	listener.Wait()

	return nil
}

func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{req: r, d: d}
}
