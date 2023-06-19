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
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/ws"
)

var eventTypes = []string{api.EventTypeLogging, api.EventTypeOperation, api.EventTypeLifecycle}
var privilegedEventTypes = []string{api.EventTypeLogging}

var eventsCmd = APIEndpoint{
	Path: "events",

	Get: APIEndpointAction{Handler: eventsGet, AccessHandler: allowAuthenticated},
}

type eventsServe struct {
	req *http.Request
	s   *state.State
}

func (r *eventsServe) Render(w http.ResponseWriter) error {
	return eventsSocket(r.s, r.req, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(s *state.State, r *http.Request, w http.ResponseWriter) error {
	// Detect project mode.
	projectName := queryParam(r, "project")
	allProjects := shared.IsTrue(queryParam(r, "all-projects"))

	if allProjects && projectName != "" {
		return api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects")
	} else if !allProjects && projectName == "" {
		projectName = project.Default
	}

	if !allProjects && projectName != project.Default {
		_, err := s.DB.GetProject(context.Background(), projectName)
		if err != nil {
			return err
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
			return api.StatusErrorf(http.StatusBadRequest, "%q isn't a supported event type", entry)
		}
	}

	if shared.StringInSlice(api.EventTypeLogging, types) && !rbac.UserIsAdmin(r) {
		return api.StatusErrorf(http.StatusForbidden, "Forbidden")
	}

	l := logger.AddContext(logger.Log, logger.Ctx{"remote": r.RemoteAddr})

	// Upgrade the connection to websocket
	conn, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Warn("Failed upgrading event connection", logger.Ctx{"err": err})
		return nil
	}

	defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

	s.Events.SetLocalLocation(s.ServerName)

	var excludeLocations []string
	// Get the current local serverName and store it for the events.
	// We do that now to avoid issues with changes to the name and to limit
	// the number of DB access to just one per connection.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
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
		l.Warn("Failed setting up event connection", logger.Ctx{"err": err})
		return nil
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
			s.Events.Inject(event, events.EventSourcePush)
		}
	}

	listenerConnection := events.NewWebsocketListenerConnection(conn)

	listener, err := s.Events.AddListener(projectName, allProjects, listenerConnection, types, excludeSources, recvFunc, excludeLocations)
	if err != nil {
		l.Warn("Failed to add event listener", logger.Ctx{"err": err})
		return nil
	}

	listener.Wait(r.Context())

	return nil
}

// swagger:operation GET /1.0/events server events_get
//
//	Get the event stream
//
//	Connects to the event API using websocket.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: type
//	    description: Event type(s), comma separated (valid types are logging, operation or lifecycle)
//	    type: string
//	    example: logging,lifecycle
//	  - in: query
//	    name: all-projects
//	    description: Retrieve instances from all projects
//	    type: boolean
//	responses:
//	  "200":
//	    description: Websocket message (JSON)
//	    schema:
//	      $ref: "#/definitions/Event"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{req: r, s: d.State()}
}
