package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/ws"
)

var eventTypes = []string{api.EventTypeLogging, api.EventTypeOperation, api.EventTypeLifecycle, api.EventTypeOVN}
var privilegedEventTypes = []string{api.EventTypeLogging, api.EventTypeOVN}

var eventsCmd = APIEndpoint{
	Path:        "events",
	MetricsType: entity.TypeServer,

	Get: APIEndpointAction{Handler: eventsGet, AccessHandler: allowAuthenticated},
}

type eventsServe struct {
	s *state.State
}

// Render starts event socket.
func (r *eventsServe) Render(w http.ResponseWriter, req *http.Request) error {
	err := eventsSocket(r.s, req, w)

	if err == nil {
		// If there was an error on Render, the callback function will be called during the error handling.
		metrics.UseMetricsCallback(req, metrics.Success)
	}

	return err
}

func (r *eventsServe) String() string {
	return "event handler"
}

// requestorMetadata is used during event filtering so that events can be filtered by [api.OperationRequestor] or
// [api.EventLifecycleRequestor] without unmarshalling unnecessary fields in event or operation metadata.
type requestorMetadata struct {
	Requestor *struct {
		Username string `json:"username"`
		Protocol string `json:"protocol"`
	} `json:"requestor"`
}

func eventsSocket(s *state.State, r *http.Request, w http.ResponseWriter) error {
	projectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return err
	}

	if !allProjects && projectName != api.ProjectDefaultName {
		_, err := s.DB.GetProject(context.Background(), projectName)
		if err != nil {
			return err
		}
	}

	// Get permission checkers required for filtering

	// This permission checker is for use with project specific lifecycle events.
	canViewProjectLifecycleEvents, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewEvents, entity.TypeProject)
	if err != nil {
		return err
	}

	// This permission checker is for use with project specific operations.
	canViewProjectOperations, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewOperations, entity.TypeProject)
	if err != nil {
		return err
	}

	// `can_view_operations` on `server` is required to view any operation event that is not project specific.
	var canViewServerOperations bool
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanViewOperations)
	if err == nil {
		canViewServerOperations = true
	} else if !auth.IsDeniedError(err) {
		return err
	}

	// `can_view_events` on `server` is required to view any of the privileged event types, or any lifecycle event that is not project specific.
	var canViewServerEvents bool
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanViewEvents)
	if err == nil {
		canViewServerEvents = true
	} else if !auth.IsDeniedError(err) {
		return err
	}

	// User requested types
	types := strings.Split(r.FormValue("type"), ",")
	if len(types) == 1 && types[0] == "" {
		// If no types were requested, return all event types the caller has permission to view.
		types = []string{}
		for _, entry := range eventTypes {
			if !canViewServerEvents && slices.Contains(privilegedEventTypes, entry) {
				continue
			}

			types = append(types, entry)
		}
	} else {
		// Otherwise, validate the provided types.
		for _, entry := range types {
			if !slices.Contains(eventTypes, entry) {
				return api.StatusErrorf(http.StatusBadRequest, "%q isn't a supported event type", entry)
			}

			if !canViewServerEvents && slices.Contains(privilegedEventTypes, entry) {
				return api.StatusErrorf(http.StatusForbidden, "Forbidden")
			}
		}
	}

	l := logger.AddContext(logger.Ctx{"remote": r.RemoteAddr})

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return err
	}

	isClusterNotification := requestor.IsClusterNotification()

	var recvFunc events.EventHandler
	var excludeSources []events.EventSource
	var excludeLocations []string
	if isClusterNotification {
		// Get the current local serverName and store it for the events.
		// We do that now to avoid issues with changes to the name and to limit
		// the number of DB access to just one per connection.
		fingerprint, err := requestor.ClusterMemberTLSCertificateFingerprint()
		if err != nil {
			l.Warn("Failed setting up event connection", logger.Ctx{"err": err})
			return nil
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			cert, err := cluster.GetCertificateByFingerprintPrefix(context.Background(), tx.Tx(), fingerprint)
			if err != nil {
				return fmt.Errorf("Failed matching client certificate to cluster member: %w", err)
			}

			// Add the cluster member client's name to the excluded locations so that we can avoid
			// looping the event back to them when they send us an event via recvFunc.
			excludeLocations = append(excludeLocations, cert.Name)
			return nil
		})
		if err != nil {
			l.Warn("Failed setting up event connection", logger.Ctx{"err": err})
			return nil
		}

		// If client is another cluster member, it will already be pulling events from other cluster
		// members so no need to also deliver forwarded events that this member receives.
		excludeSources = append(excludeSources, events.EventSourcePull)

		recvFunc = func(event api.Event) {
			// Inject event received via push from event listener client so its forwarded to
			// other event hub members (if operating in event hub mode).
			s.Events.Inject(event, events.EventSourcePush)
		}
	}

	filter := func(log logger.Logger, event api.Event) bool {
		l = log.AddContext(logger.Ctx{"type": event.Type, "location": event.Location, "project": event.Project})

		// Privileged events require `can_view_events` on `server.
		if slices.Contains(privilegedEventTypes, event.Type) {
			return canViewServerEvents
		}

		switch event.Type {
		case api.EventTypeLifecycle:
			// Lifecycle events that are not project specific require `can_view_events` on `server`.
			if event.Project == "" {
				return canViewServerEvents
			}

			// Otherwise check if the caller has `can_view_lifecycle_events` on the project.
			if canViewProjectLifecycleEvents(entity.ProjectURL(event.Project)) {
				return true
			}

		case api.EventTypeOperation:
			// Operations that are not project specific require `can_view_operations` on `server`.
			if event.Project == "" {
				return canViewServerOperations
			}

			// Otherwise check if the caller has `can_view_operations` on the project.
			if canViewProjectOperations(entity.ProjectURL(event.Project)) {
				return true
			}

		default:
			// We don't expect any other event types at this point
			l.Warn("Received unexpected event type")
			return false
		}

		// At this point the caller does not have permission to view the event via group membership or project.
		// Check the event or operation requestor to see if they are the identity that triggered the event.

		// Unmarshal the requestor from the event metadata.
		var m requestorMetadata
		err := json.Unmarshal(event.Metadata, &m)
		if err != nil {
			l.Error("Failed to unmarshal event metadata during requestor filtering")
			return false
		}

		if m.Requestor == nil {
			return false
		}

		// Allow the event if the same requestor is connected.
		if m.Requestor.Username == requestor.CallerUsername() && m.Requestor.Protocol == requestor.CallerProtocol() {
			return true
		}

		// Otherwise, filter it out.
		return false
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
	listener, err := s.Events.AddListener(projectName, allProjects, filter, listenerConnection, types, excludeSources, recvFunc, excludeLocations)
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
