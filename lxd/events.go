package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/rbac"
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
	projectName := projectParam(r)
	types := strings.Split(r.FormValue("type"), ",")
	if len(types) == 1 && types[0] == "" {
		types = []string{}
		for _, entry := range eventTypes {
			if !rbac.UserIsAdmin(r) && shared.StringInSlice(entry, privilegedEventTypes) {
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

	if shared.StringInSlice("logging", types) && !rbac.UserIsAdmin(r) {
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
	listener, err := d.events.AddListener(projectName, c, types, serverName, isClusterNotification(r))
	if err != nil {
		return err
	}

	logger.Debugf("New event listener: %s", listener.ID())
	listener.Wait(r.Context())
	logger.Debugf("Event listener finished: %s", listener.ID())

	return nil
}

// swagger:operation GET /1.0/events server events_get
//
// Get the event stream
//
// Connects to the event API using websocket.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: type
//     description: Event type(s), comma separated (valid types are logging, operation or lifecycle)
//     type: string
//     example: logging,lifecycle
// responses:
//   "200":
//     description: Websocket message (JSON)
//     schema:
//       $ref: "#/definitions/Event"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{req: r, d: d}
}
