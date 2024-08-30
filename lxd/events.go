package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/ws"
)

var eventTypes = []string{api.EventTypeLogging, api.EventTypeOperation, api.EventTypeLifecycle, api.EventTypeOVN}
var privilegedEventTypes = []string{api.EventTypeLogging}

var eventsCmd = APIEndpoint{
	Path: "events",

	Get: APIEndpointAction{Handler: eventsGet, AccessHandler: allowProjectResourceList},
}

type eventsServe struct {
	s *state.State
}

// Render starts event socket.
func (r *eventsServe) Render(w http.ResponseWriter, req *http.Request) error {
	err := eventsSocket(r.s, req, w)

	if err == nil {
		// If there was an error on Render, the callback function will be called during the error handling.
		request.MetricsCallback(req, metrics.Success)
	}

	return err
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(s *state.State, r *http.Request, w http.ResponseWriter) error {
	// Detect project mode.
	projectName := request.QueryParam(r, "project")
	allProjects := shared.IsTrue(request.QueryParam(r, "all-projects"))

	if allProjects && projectName != "" {
		return api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects")
	} else if !allProjects && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	if !allProjects && projectName != api.ProjectDefaultName {
		_, err := s.DB.GetProject(context.Background(), projectName)
		if err != nil {
			return err
		}
	}

	// Notes on authorization for events:
	// - Checks are currently performed at the project level. Fine-grained auth uses `can_view_events` on the project,
	//   TLS auth checks if a restricted identity has access to the project against which the event is defined.
	// - If project "foo" does not have a particular feature enabled, say 'features.networks', if a network is updated
	//   via project "foo", no events will be emitted in project "foo" relating to the network. They will only be emitted
	//   in project "default". In order to get all related events, TLS users must be granted access to the default project,
	//   fine-grained users can be granted `can_view_events` on the default project. Both must call the events API with
	//   `all-projects=true`.
	var projectPermissionFunc auth.PermissionChecker
	if projectName != "" {
		err := s.Authorizer.CheckPermission(r.Context(), entity.ProjectURL(projectName), auth.EntitlementCanViewEvents)
		if err != nil {
			return err
		}
	} else if allProjects {
		var err error
		projectPermissionFunc, err = s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewEvents, entity.TypeProject)
		if err != nil {
			return err
		}
	}

	canViewPrivilegedEvents := s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanViewPrivilegedEvents) == nil

	types := strings.Split(r.FormValue("type"), ",")
	if len(types) == 1 && types[0] == "" {
		types = []string{}
		for _, entry := range eventTypes {
			if !canViewPrivilegedEvents && shared.ValueInSlice(entry, privilegedEventTypes) {
				continue
			}

			types = append(types, entry)
		}
	}

	// Validate event types.
	for _, entry := range types {
		if !shared.ValueInSlice(entry, eventTypes) {
			return api.StatusErrorf(http.StatusBadRequest, "%q isn't a supported event type", entry)
		}
	}

	if shared.ValueInSlice(api.EventTypeLogging, types) && !canViewPrivilegedEvents {
		return api.StatusErrorf(http.StatusForbidden, "Forbidden")
	}

	l := logger.AddContext(logger.Ctx{"remote": r.RemoteAddr})

	var excludeLocations []string
	// Get the current local serverName and store it for the events.
	// We do that now to avoid issues with changes to the name and to limit
	// the number of DB access to just one per connection.
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	// Upgrade the connection to websocket as late as possible.
	// This is because the client will assume it's getting events as soon as the upgrade is performed.
	conn, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Warn("Failed upgrading event connection", logger.Ctx{"err": err})
		return nil
	}

	defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

	listenerConnection := events.NewWebsocketListenerConnection(conn)
	listener, err := s.Events.AddListener(projectName, allProjects, projectPermissionFunc, listenerConnection, types, excludeSources, recvFunc, excludeLocations)
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
	return &eventsServe{s: d.State()}
}
