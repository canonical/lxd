package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ws"
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
	allProjects := shared.IsTrue(queryParam(r, "all-projects"))
	projectQueryParam := queryParam(r, "project")
	if allProjects && projectQueryParam != "" {
		response.BadRequest(fmt.Errorf("Cannot specify a project when requesting events for all projects"))
		return nil
	}

	var projectName string
	if !allProjects {
		if projectQueryParam == "" {
			projectName = project.Default
		} else {
			projectName = projectQueryParam

			_, err := d.db.GetProject(context.Background(), projectName)
			if err != nil {
				if response.IsNotFoundError(err) {
					response.BadRequest(fmt.Errorf("Project %q not found", projectName)).Render(w)
				}

				return err
			}
		}
	}

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
	c, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer c.Close() // This ensures the go routine below is ended when this function ends.

	var excludeLocations []string
	// Get the current local serverName and store it for the events.
	// We do that now to avoid issues with changes to the name and to limit
	// the number of DB access to just one per connection.
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		serverName, err := tx.GetLocalNodeName()
		if err != nil {
			return err
		}

		d.events.SetLocalLocation(serverName)

		if isClusterNotification(r) {
			ctx := r.Context()

			// Try and match cluster member certificate fingerprint to member name.
			fingerprint, found := ctx.Value(request.CtxUsername).(string)
			if found {
				cert, err := cluster.GetCertificateByFingerprintPrefix(context.Background(), tx.Tx(), fingerprint)
				if err != nil {
					return fmt.Errorf("Failed matching client certificate to cluster member: %w", err)
				}

				// Add the cluster member client's name to the excluded locations so that we can avoid
				// looping the event back to them when they send us an event via recvFunc.
				excludeLocations = append(excludeLocations, cert.Name)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	var recvFunc events.EventHandler
	var excludeSources []events.EventSource
	if isClusterNotification(r) {
		// If client is another cluster member, it will already be pulling events from other cluster
		// members so no need to also deliver forwarded events that this member receives.
		excludeSources = append(excludeSources, events.EventSourcePull)

		recvFunc = func(event api.Event) {
			// Inject event received via push from event listener client so its forwarded to
			// other event hub members (if operating in event hub mode).
			d.events.Inject(event, events.EventSourcePush)
		}
	}

	listener, err := d.events.AddListener(projectName, allProjects, c, types, excludeSources, recvFunc, excludeLocations)
	if err != nil {
		return err
	}

	listener.Wait(r.Context())

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
