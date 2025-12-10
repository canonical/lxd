package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

var operationCmd = APIEndpoint{
	Path:        "operations/{id}",
	MetricsType: entity.TypeOperation,

	Delete: APIEndpointAction{Handler: operationDelete, AccessHandler: allowAuthenticated},
	Get:    APIEndpointAction{Handler: operationGet, AccessHandler: allowAuthenticated},
}

var operationsCmd = APIEndpoint{
	Path:        "operations",
	MetricsType: entity.TypeOperation,

	Get: APIEndpointAction{Handler: operationsGet, AccessHandler: allowProjectResourceList(true)},
}

var operationWait = APIEndpoint{
	Path:        "operations/{id}/wait",
	MetricsType: entity.TypeOperation,

	Get: APIEndpointAction{Handler: operationWaitGet, AllowUntrusted: true},
}

var operationWebsocket = APIEndpoint{
	Path:        "operations/{id}/websocket",
	MetricsType: entity.TypeOperation,

	Get: APIEndpointAction{Handler: operationWebsocketGet, AllowUntrusted: true},
}

// DurableOperations is the table of durable operations handlers.
// This is needed so that we can always find the right handlers based on the operation type.
// We want this in the main package so the table can contain handlers from various other packages.
var DurableOperations = operations.DurableOperationTable{
	operationtype.Wait: waitHandlerOperationRunHook,
}

// pendingInstanceOperations returns a map of instance URLs to operations that are currently running.
// This is used to determine if an instance is busy and should not be shut down immediately.
func pendingInstanceOperations() (map[string]*operations.Operation, error) {
	// Get all the operations
	ops := operations.Clone()
	res := make(map[string]*operations.Operation)

	for _, op := range ops {
		if op.Status() != api.Running || op.Class() == operations.OperationClassToken {
			continue
		}

		_, opAPI, err := op.Render()
		if err != nil {
			return nil, fmt.Errorf("Failed to render an operation while listing all operations: %w", err)
		}

		// If the current operations has a hold on some resources, we keep track of them in the `resourceMap`.
		// This is used to mark instances and storage volumes as busy to avoid shutting them down / unmounting them prematurely.
		// This avoids the situation where a single very long running operation can block the shutdown of unrelated instances and the unmount of unrelated storage volumes.
		for resourceName, resourceEntries := range opAPI.Resources {
			if resourceName != "instances" {
				continue
			}

			for _, rawURL := range resourceEntries {
				u := api.NewURL()
				parsedURL, err := u.Parse(rawURL)
				if err != nil {
					logger.Warn("Failed to parse raw URL", logger.Ctx{"rawURL": rawURL, "err": err})
					continue
				}

				entityType, projectName, location, pathArgs, err := entity.ParseURL(*parsedURL)
				if err != nil {
					logger.Warn("Failed to parse URL into a LXD entity", logger.Ctx{"url": parsedURL.String(), "err": err})
					continue
				}

				if entityType == entity.TypeInstance {
					entityURL, err := entityType.URL(projectName, location, pathArgs...)
					if err != nil {
						logger.Warn("Failed to generate entity URL", logger.Ctx{"entityType": entityType, "projectName": projectName, "location": location, "pathArgs": pathArgs, "err": err})
						continue
					}

					res[entityURL.String()] = op
				}
			}
		}
	}

	return res, nil
}

// API functions

// swagger:operation GET /1.0/operations/{id} operations operation_get
//
//	Get the operation state
//
//	Gets the operation state.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Operation
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	var body *api.Operation
	var address string
	var dbLocation *string
	opFound := false
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		opFound = true
	} else {
		var operation dbCluster.Operation
		// If it's not running on this node, load the operation from the database.
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			filter := dbCluster.OperationFilter{UUID: &id}
			ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
			if err != nil {
				return err
			}

			// Make sure we have loaded exactly one operation from the DB.
			switch len(ops) {
			case 0:
				return api.StatusErrorf(http.StatusNotFound, "Operation not found")
			case 1:
				operation = ops[0]
			default:
				return errors.New("More than one operation matches")
			}

			address = operation.NodeAddress

			// If it's durable operation, try to load it from the database.
			if operation.Class == int64(operations.OperationClassDurable) {
				projectID := int(*operation.ProjectID)
				filter := dbCluster.ProjectFilter{ID: &projectID}
				projects, err := dbCluster.GetProjects(r.Context(), tx.Tx(), filter)
				if err != nil {
					return err
				}

				// Make sure we have loaded exactly one project from the DB.
				var project dbCluster.Project
				switch len(projects) {
				case 0:
					return api.StatusErrorf(http.StatusNotFound, "Project not found")
				case 1:
					project = projects[0]
				default:
					return errors.New("More than one project matches")
				}

				ni, err := tx.GetNodeByID(ctx, operation.NodeID)
				if err != nil {
					return err
				}

				dbLocation = &ni.Name

				op, err = operations.NewDurableOperation(r.Context(), tx.Tx(), s, &operation, project.Name)
				if err == nil {
					opFound = true
				}

				return err
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// If we found the operation locally, or we were able to reconstruct a durable operation from the db,
	// render and return it.
	if opFound {
		err := checkOperationViewAccess(r.Context(), op, s.Authorizer, "")
		if err != nil {
			return response.SmartError(err)
		}

		_, body, err = op.Render()
		if err != nil {
			return response.SmartError(err)
		}

		// The [operations.Operation] doesn't contain the node where the operation is running.
		// If we're loading durable operations from the DB, we need to set the location here.
		if dbLocation != nil {
			body.Location = *dbLocation
		}

		return response.SyncResponse(true, body)
	}

	// Otherwise forward the request to the node running the operation.
	client, err := cluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client)
}

// swagger:operation DELETE /1.0/operations/{id} operations operation_delete
//
//	Cancel the operation
//
//	Cancels the operation if supported.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		projectName := op.Project()
		if projectName == "" {
			projectName = api.ProjectDefaultName
		}

		requestor, err := request.GetRequestor(r.Context())
		if err != nil {
			return response.SmartError(err)
		}

		// Allow cancellation only if the caller is equal or the caller is a server admin.
		// Rather than using the "admin" entitlement, we use `can_edit` because this is used for arbitrary editing of
		// server config, warnings, cluster membership etc.
		if !requestor.CallerIsEqual(op.Requestor()) {
			err := s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanEdit)
			if err != nil {
				return response.SmartError(err)
			}
		}

		if !op.IsRunning() {
			return response.BadRequest(errors.New("Only running operations can be cancelled"))
		}

		op.Cancel()
		s.Events.SendLifecycle(projectName, lifecycle.OperationCancelled.Event(op, request.CreateRequestor(r.Context()), nil))

		_ = op.Wait(r.Context())
		return response.EmptySyncResponse
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var operation dbCluster.Operation
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return errors.New("More than one operation matches")
		}

		operation = ops[0]
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Don't forward the request if we don't have where to forward it to.
	if operation.NodeAddress == "" || operation.NodeAddress == s.LocalConfig.ClusterAddress() {
		if operation.Class == int64(operations.OperationClassDurable) && api.StatusCode(operation.Status).IsFinal() {
			return response.BadRequest(errors.New("Durable operation is already finalized"))
		}

		return response.SmartError(fmt.Errorf("Operation ID %q is not running on this node", id))
	}

	client, err := cluster.Connect(r.Context(), operation.NodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client)
}

// operationCancel cancels an operation that exists on any member.
func operationCancel(ctx context.Context, s *state.State, projectName string, op *api.Operation) error {
	// Check if operation is local and if so, cancel it.
	localOp, _ := operations.OperationGetInternal(op.ID)
	if localOp != nil {
		localOp.Cancel()
		s.Events.SendLifecycle(projectName, lifecycle.OperationCancelled.Event(localOp, request.CreateRequestor(ctx), nil))
		_ = localOp.Wait(ctx)

		return nil
	}

	// If not found locally, try connecting to remote member to delete it.
	var memberAddress string
	var err error
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &op.ID}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading operation %q: %w", op.ID, err)
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return errors.New("More than one operation matches")
		}

		operation := ops[0]

		memberAddress = operation.NodeAddress
		return nil
	})
	if err != nil {
		return err
	}

	client, err := cluster.Connect(ctx, memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), true)
	if err != nil {
		return fmt.Errorf("Failed to connect to %q: %w", memberAddress, err)
	}

	err = client.UseProject(projectName).DeleteOperation(op.ID)
	if err != nil {
		return fmt.Errorf("Failed to delete remote operation %q on %q: %w", op.ID, memberAddress, err)
	}

	return nil
}

// swagger:operation GET /1.0/operations operations operations_get
//
//  Get the operations
//
//  Returns a JSON object of operation type to operation list (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve operations from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: object
//            additionalProperties:
//              type: array
//              items:
//                type: string
//            description: JSON object of operation types to operation URLs
//            example: |-
//              {
//                "running": [
//                  "/1.0/operations/6916c8a6-9b7d-4abd-90b3-aedfec7ec7da"
//                ]
//              }
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/operations?recursion=1 operations operations_get_recursion1
//
//	Get the operations
//
//	Returns a list of operations (structs).
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
//	    name: all-projects
//	    description: Retrieve operations from all projects
//	    type: boolean
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of operations
//	          items:
//	            $ref: "#/definitions/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	projectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	var projectID int64
	if !allProjects && projectName != api.ProjectDefaultName {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			projectID, err = dbCluster.GetProjectID(ctx, tx.Tx(), projectName)
			if err != nil {
				return fmt.Errorf("Failed to get project: %w", err)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	offlineThreshold := s.GlobalConfig.OfflineThreshold()
	memberOnline := func(member *db.NodeInfo) bool {
		if member.IsOffline(offlineThreshold) {
			logger.Warn("Excluding offline member from operations list", logger.Ctx{"member": member.Name, "address": member.Address, "ID": member.ID, "lastHeartbeat": member.Heartbeat})
			return false
		}

		return true
	}

	canViewProjectOperations, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewOperations, entity.TypeProject)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to get operation permission checker: %w", err))
	}

	// Not all operations have a project. Operations that don't have a project should be considered "server level".
	var canViewServerOperations bool
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanViewOperations)
	if err == nil {
		canViewServerOperations = true
	} else if !auth.IsDeniedError(err) {
		return response.SmartError(fmt.Errorf("Failed to check caller access to server operations: %w", err))
	}

	// We'll start by loading all operations from the database and sorting them out to durable operations and other classes.
	// Durable operations are directly loaded from the database and converted into their API representation.
	// For all the other operations we'll compile a list of nodes running these and get them directly from those nodes via cluster notifications.
	var durableOps []api.Operation
	membersWithOps := make(map[string]struct{})
	var members []db.NodeInfo
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load all operations for the project (or all projects).
		var filter []dbCluster.OperationFilter
		if !allProjects && projectName != api.ProjectDefaultName {
			filter = append(filter, dbCluster.OperationFilter{ProjectID: &projectID})
		}

		dbOps, err := dbCluster.GetOperations(ctx, tx.Tx(), filter...)
		if err != nil {
			return fmt.Errorf("Failed getting operations: %w", err)
		}

		// Load cluster members.
		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		for _, dbOp := range dbOps {
			if operations.OperationClass(dbOp.Class) == operations.OperationClassDurable {
				// Omit operations that don't have a project if the caller does not have access.
				if dbOp.ProjectID == nil && !canViewServerOperations {
					continue
				}

				// Durable operation, convert to API representation now.
				op, err := operations.NewDurableOperation(ctx, tx.Tx(), s, &dbOp, projectName)
				if err != nil {
					return fmt.Errorf("Failed loading durable operation ID %q: %w", dbOp.UUID, err)
				}

				// Omit operations if the caller does not have `can_view_operations` on the operations' project and the caller is not the operation owner.
				if !canViewProjectOperations(entity.ProjectURL(projectName)) && !requestor.CallerIsEqual(op.Requestor()) {
					continue
				}

				_, apiOp, err := op.Render()
				if err != nil {
					return fmt.Errorf("Failed converting durable operation ID %q to API representation: %w", dbOp.UUID, err)
				}

				// The [operations.Operation] doesn't contain the node where the operation is running.
				// If we're loading durable operations from the DB, we need to set the location here.
				apiOp.Location = ""
				for _, memberInMembers := range members {
					if memberInMembers.ID == dbOp.NodeID {
						apiOp.Location = memberInMembers.Name
					}
				}

				durableOps = append(durableOps, *apiOp)
				continue
			}

			// Non-durable operation, note the node running it.
			membersWithOps[dbOp.NodeAddress] = struct{}{}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	localOperationURLs := func() (shared.Jmap, error) {
		// Get all the operations.
		localOps := operations.Clone()

		// Build a list of URLs.
		body := shared.Jmap{}

		for _, v := range localOps {
			if v.Class() == operations.OperationClassDurable {
				continue
			}

			operationProject := v.Project()
			if !allProjects && operationProject != "" && operationProject != projectName {
				continue
			}

			// Omit operations that don't have a project if the caller does not have access to server operations.
			if operationProject == "" && !canViewServerOperations {
				continue
			}

			// Omit operations if the caller does not have `can_view_operations` on the operations' project and the caller is not the operation owner.
			if !canViewProjectOperations(entity.ProjectURL(operationProject)) && !requestor.CallerIsEqual(v.Requestor()) {
				continue
			}

			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]string, 0)
			}

			body[status] = append(body[status].([]string), v.URL())
		}

		return body, nil
	}

	localOperations := func() (shared.Jmap, error) {
		// Get all the operations.
		localOps := operations.Clone()

		// Build a list of operations.
		body := shared.Jmap{}

		for _, v := range localOps {
			if v.Class() == operations.OperationClassDurable {
				continue
			}

			operationProject := v.Project()
			if !allProjects && operationProject != "" && operationProject != projectName {
				continue
			}

			// Omit operations that don't have a project if the caller does not have access.
			if operationProject == "" && !canViewServerOperations {
				continue
			}

			// Omit operations if the caller does not have `can_view_operations` on the operations' project and the caller is not the operation owner.
			if !canViewProjectOperations(entity.ProjectURL(operationProject)) && !requestor.CallerIsEqual(v.Requestor()) {
				continue
			}

			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]*api.Operation, 0)
			}

			_, op, err := v.Render()
			if err != nil {
				return nil, err
			}

			body[status] = append(body[status].([]*api.Operation), op)
		}

		return body, nil
	}

	recursion := util.IsRecursionRequest(r)

	// Check if called from a cluster node.
	if requestor.IsClusterNotification() {
		// Only return the local data.
		if recursion {
			// Recursive queries.
			body, err := localOperations()
			if err != nil {
				return response.InternalError(err)
			}

			return response.SyncResponse(true, body)
		}

		// Normal queries
		body, err := localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}

		return response.SyncResponse(true, body)
	}

	// Start with local operations.
	var md shared.Jmap

	if recursion {
		md, err = localOperations()
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		md, err = localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}
	}

	// Merge with the durable operations data.
	for _, op := range durableOps {
		status := strings.ToLower(op.Status)

		_, ok := md[status]
		if !ok {
			if recursion {
				md[status] = make([]*api.Operation, 0)
			} else {
				md[status] = make([]string, 0)
			}
		}

		if recursion {
			md[status] = append(md[status].([]*api.Operation), &op)
		} else {
			md[status] = append(md[status].([]string), "/1.0/operations/"+op.ID)
		}
	}

	// If not clustered, then just return local operations.
	if !s.ServerClustered {
		return response.SyncResponse(true, md)
	}

	// Get local address.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	// Load non-durable operations from other cluster members.
	networkCert := s.Endpoints.NetworkCert()
	for memberAddress := range membersWithOps {
		if memberAddress == localClusterAddress {
			continue
		}

		var member *db.NodeInfo
		for _, memberInMembers := range members {
			if memberInMembers.Address == memberAddress {
				member = &memberInMembers
			}
		}

		// If we didn't find the member in the list, skip it.
		if member == nil {
			logger.Warn("Member with operations not found in the cluster member list", logger.Ctx{"address": memberAddress})
			continue
		}

		if !memberOnline(member) {
			continue
		}

		// Connect to the remote server. Use notify=true to only get local operations on remote member.
		client, err := cluster.Connect(r.Context(), memberAddress, networkCert, s.ServerCert(), true)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed connecting to member %q: %w", memberAddress, err))
		}

		// Get operation data.
		var memberOps []api.Operation
		if allProjects {
			memberOps, err = client.GetOperationsAllProjects()
		} else {
			memberOps, err = client.UseProject(projectName).GetOperations()
		}

		if err != nil {
			logger.Warn("Failed getting operations from member", logger.Ctx{"address": memberAddress, "err": err})
			continue
		}

		// Merge with existing data.
		for _, op := range memberOps {
			status := strings.ToLower(op.Status)

			_, ok := md[status]
			if !ok {
				if recursion {
					md[status] = make([]*api.Operation, 0)
				} else {
					md[status] = make([]string, 0)
				}
			}

			if recursion {
				md[status] = append(md[status].([]*api.Operation), &op)
			} else {
				md[status] = append(md[status].([]string), "/1.0/operations/"+op.ID)
			}
		}
	}

	return response.SyncResponse(true, md)
}

// operationsGetByType gets all operations for a project and type.
func operationsGetByType(ctx context.Context, s *state.State, projectName string, opType operationtype.Type) ([]*api.Operation, error) {
	ops := make([]*api.Operation, 0)

	// Get local operations for project.
	for _, op := range operations.Clone() {
		if op.Project() != projectName || op.Type() != opType {
			continue
		}

		_, apiOp, err := op.Render()
		if err != nil {
			return nil, fmt.Errorf("Failed converting local operation %q to API representation: %w", op.ID(), err)
		}

		ops = append(ops, apiOp)
	}

	// Return just local operations if not clustered.
	if !s.ServerClustered {
		return ops, nil
	}

	// Get all operations of the specified type in project.
	var members []db.NodeInfo
	memberOps := make(map[string]map[string]dbCluster.Operation)
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		ops, err := tx.GetOperationsOfType(ctx, projectName, opType)
		if err != nil {
			return fmt.Errorf("Failed getting operations for project %q and type %d: %w", projectName, opType, err)
		}

		// Group operations by member address and UUID.
		for _, op := range ops {
			if memberOps[op.NodeAddress] == nil {
				memberOps[op.NodeAddress] = make(map[string]dbCluster.Operation)
			}

			memberOps[op.NodeAddress][op.UUID] = op
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Get local address.
	localClusterAddress := s.LocalConfig.ClusterAddress()
	offlineThreshold := s.GlobalConfig.OfflineThreshold()

	memberOnline := func(memberAddress string) bool {
		for _, member := range members {
			if member.Address == memberAddress {
				if member.IsOffline(offlineThreshold) {
					logger.Warn("Excluding offline member from operations by type list", logger.Ctx{"member": member.Name, "address": member.Address, "ID": member.ID, "lastHeartbeat": member.Heartbeat, "opType": opType})
					return false
				}

				return true
			}
		}

		return false
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	for memberAddress := range memberOps {
		if memberAddress == localClusterAddress {
			continue
		}

		if !memberOnline(memberAddress) {
			continue
		}

		// Connect to the remote server. Use notify=true to only get local operations on remote member.
		client, err := cluster.Connect(ctx, memberAddress, networkCert, serverCert, true)
		if err != nil {
			return nil, fmt.Errorf("Failed connecting to member %q: %w", memberAddress, err)
		}

		// Get all remote operations in project.
		remoteOps, err := client.UseProject(projectName).GetOperations()
		if err != nil {
			logger.Warn("Failed getting operations from member", logger.Ctx{"address": memberAddress, "err": err})
			continue
		}

		for _, op := range remoteOps {
			// Exclude remote operations that don't have the desired type.
			if memberOps[memberAddress][op.ID].Type != opType {
				continue
			}

			ops = append(ops, &op)
		}
	}

	return ops, nil
}

// swagger:operation GET /1.0/operations/{id}/wait?public operations operation_wait_get_untrusted
//
//  Wait for the operation
//
//  Waits for the operation to reach a final state (or timeout) and retrieve its final state.
//
//  When accessed by an untrusted user, the secret token must be provided.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: secret
//      description: Authentication token
//      type: string
//      example: random-string
//    - in: query
//      name: timeout
//      description: Timeout in seconds (-1 means never)
//      type: integer
//      example: -1
//  responses:
//    "200":
//      description: Operation
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/Operation"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/operations/{id}/wait operations operation_wait_get
//
//	Wait for the operation
//
//	Waits for the operation to reach a final state (or timeout) and retrieve its final state.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: timeout
//	    description: Timeout in seconds (-1 means never)
//	    type: integer
//	    example: -1
//	responses:
//	  "200":
//	    description: Operation
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationWaitGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	secret := r.FormValue("secret")

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	trusted := requestor.IsTrusted()

	if !trusted && secret == "" {
		return response.Forbidden(nil)
	}

	timeoutSecs, err := shared.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return response.InternalError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		err := checkOperationViewAccess(r.Context(), op, s.Authorizer, secret)
		if err != nil {
			return response.SmartError(err)
		}

		var ctx context.Context
		var cancel context.CancelFunc

		// If timeout is -1, it will wait indefinitely otherwise it will timeout after timeoutSecs.
		if timeoutSecs > -1 {
			ctx, cancel = context.WithDeadline(r.Context(), time.Now().Add(time.Second*time.Duration(timeoutSecs)))
		} else {
			ctx, cancel = context.WithCancel(r.Context())
		}

		waitResponse := func(w http.ResponseWriter) error {
			defer cancel()

			// Write header to avoid client side timeouts.
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			f, ok := w.(http.Flusher)
			if ok {
				f.Flush()
			}

			// Wait for the operation.
			err = op.Wait(ctx)
			if err != nil {
				_ = response.SmartError(err).Render(w, r)
				return nil
			}

			_, body, err := op.Render()
			if err != nil {
				_ = response.SmartError(err).Render(w, r)
				return nil
			}

			_ = response.SyncResponse(true, body).Render(w, r)
			return nil
		}

		return response.ManualResponse(waitResponse)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return errors.New("More than one operation matches")
		}

		operation := ops[0]

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	client, err := cluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client)
}

func checkOperationViewAccess(ctx context.Context, op *operations.Operation, authorizer auth.Authorizer, secret string) error {
	// If a secret is provided and it matches the operation, allow access.
	if secret != "" {
		// Assert opSecret is a string then convert to []byte for constant time comparison.
		opSecret, ok := op.Metadata()["secret"]
		if ok {
			opSecretStr, ok := opSecret.(string)
			if ok && subtle.ConstantTimeCompare([]byte(opSecretStr), []byte(secret)) == 1 {
				return nil
			}
		}
	}

	// There must be a requestor.
	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return err
	}

	// The caller must be trusted.
	if !requestor.IsTrusted() {
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	// Allow view access if the caller is the requestor.
	if requestor.CallerIsEqual(op.Requestor()) {
		return nil
	}

	// Otherwise, perform access check based on whether the operation is project specific.
	operationProject := op.Project()
	var entityURL *api.URL
	if operationProject == "" {
		// If not project specific, this is a server level operation.
		entityURL = entity.ServerURL()
	} else {
		// If project specific, check `can_view_operations` on the operations' project.
		entityURL = entity.ProjectURL(operationProject)
	}

	return authorizer.CheckPermission(ctx, entityURL, auth.EntitlementCanViewOperations)
}

// swagger:operation GET /1.0/operations/{id}/websocket?public operations operation_websocket_get_untrusted
//
//  Get the websocket stream
//
//  Connects to an associated websocket stream for the operation.
//  This should almost never be done directly by a client, instead it's
//  meant for LXD to LXD communication with the client only relaying the
//  connection information to the servers.
//
//  The untrusted endpoint is used by the target server to connect to the source server.
//  Authentication is performed through the secret token.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: secret
//      description: Authentication token
//      type: string
//      example: random-string
//  responses:
//    "200":
//      description: Websocket operation messages (dependent on operation)
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/operations/{id}/websocket operations operation_websocket_get
//
//	Get the websocket stream
//
//	Connects to an associated websocket stream for the operation.
//	This should almost never be done directly by a client, instead it's
//	meant for LXD to LXD communication with the client only relaying the
//	connection information to the servers.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: secret
//	    description: Authentication token
//	    type: string
//	    example: random-string
//	responses:
//	  "200":
//	    description: Websocket operation messages (dependent on operation)
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationWebsocketGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		return operations.OperationWebSocket(op)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	secret := r.FormValue("secret")
	if secret == "" {
		return response.BadRequest(errors.New("Missing websocket secret"))
	}

	var address string
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return errors.New("More than one operation matches")
		}

		operation := ops[0]

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	client, err := cluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	source, err := client.GetOperationWebsocket(id, secret)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.ForwardedOperationWebSocket(id, source)
}

func autoRemoveOrphanedOperationsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		leaderInfo, err := s.LeaderInfo()
		if err != nil {
			logger.Error("Failed to get leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if !leaderInfo.Clustered {
			return
		}

		if !leaderInfo.Leader {
			logger.Debug("Skipping remove orphaned operations task since we're not leader")
			return
		}

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return autoRemoveOrphanedOperations(ctx, s)
		}

		args := operations.OperationArgs{
			Type:    operationtype.RemoveOrphanedOperations,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		}

		op, err := operations.CreateServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating remove orphaned operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting remove orphaned operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed removing orphaned operations", logger.Ctx{"err": err})
			return
		}
	}

	// All the cluster tasks are starting at the daemon init, at which time the cluster heartbeats
	// have not yet been updated. The autoRemoveOrphanedOperations() might start deleting operations
	// which are just starting on other nodes. To avoid this, we skip the first run of this
	// task, allowing time for the heartbeats to be updated.
	return f, task.Hourly(task.SkipFirst)
}

// autoRemoveOrphanedOperations removes old operations from offline members. Operations can be left
// behind if a cluster member abruptly becomes unreachable. If the affected cluster members comes
// back online, these operations won't be cleaned up. We therefore need to periodically clean up
// such operations.
func autoRemoveOrphanedOperations(ctx context.Context, s *state.State) error {
	logger.Debug("Removing orphaned operations across the cluster")

	offlineThreshold := s.GlobalConfig.OfflineThreshold()

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		for _, member := range members {
			// Skip online nodes
			if !member.IsOffline(offlineThreshold) {
				continue
			}

			err = dbCluster.DeleteNonDurableOperations(ctx, tx.Tx(), member.ID)
			if err != nil {
				return fmt.Errorf("Failed to delete operations: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to remove orphaned operations: %w", err)
	}

	logger.Debug("Done removing orphaned operations across the cluster")

	return nil
}

// PruneExpiredDurableOperationsTask returns a task function and schedule that
// is used to prune expired durable operations from the database.
func pruneExpiredDurableOperationsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		leaderInfo, err := s.LeaderInfo()
		if err != nil {
			logger.Error("Failed getting leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if !leaderInfo.Leader {
			logger.Debug("Skipping pruning expired durable operations since we're not leader")
			return
		}

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return operations.PruneExpiredDurableOperations(ctx, s)
		}

		args := operations.OperationArgs{
			Type:    operationtype.PruneExpiredDurableOperations,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		}

		op, err := operations.CreateServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating prune expired durable operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting prune expired durable operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed pruning expired durable operations", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Hourly()
}

func syncDurableOperationsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		dbOps, err := operations.LoadDurableOperationsFromNode(ctx, s, s.DB.Cluster.GetNodeID())
		if err != nil {
			logger.Warnf("Failed loading durable operations on node: %v", err)
			return
		}

		// Get the list of local durable operations.
		localOps := operations.Clone()
		// Convert it to a map for easier lookup.
		localOpsMap := make(map[string]*operations.Operation)
		for _, op := range localOps {
			if op.Class() != operations.OperationClassDurable {
				continue
			}

			localOpsMap[op.ID()] = op
		}

		// Ensure that all durable operations in the database are running locally.
		for _, op := range dbOps {
			if op.Status().IsFinal() {
				// Operation is in a final state, no need to create it.
				continue
			}

			// If the operation is already running locally, everything is great.
			_, ok := localOpsMap[op.ID()]
			if ok {
				continue
			}

			// Operation is not running locally, we need to restart it.
			logger.Warnf("Restarting durable operation %q", op.ID())
			operations.RestartDurableOperation(s, op)
		}

		// Now we put the database operations in a map, and ensure that all local
		// durable operations are present in the database.
		dbOpsMap := make(map[string]*operations.Operation)
		for _, dbOp := range dbOps {
			dbOpsMap[dbOp.ID()] = dbOp
		}

		for _, op := range localOpsMap {
			if op.Status().IsFinal() {
				// Operation is in a final state, no need to cancel it.
				continue
			}

			// If the local operation is written in the DB, everything is great.
			_, ok := dbOpsMap[op.ID()]
			if ok {
				continue
			}

			// Operation is not in the DB, we need to cancel it.
			logger.Warnf("Cancelling local durable operation %q as it's not running on this node per the database", op.ID())
			operations.CancelLocalDurableOperation(op)
		}
	}

	return f, task.Every(time.Minute)
}

// operationWaitPost represents the fields of a request to register a dummy operation.
type operationWaitPost struct {
	Duration  string                    `json:"duration" yaml:"duration"`
	OpClass   operations.OperationClass `json:"op_class" yaml:"op_class"`
	OpType    operationtype.Type        `json:"op_type" yaml:"op_type"`
	Resources map[string][]string       `json:"resources" yaml:"resources"`
}

func waitHandlerOperationRunHook(ctx context.Context, op *operations.Operation) error {
	inputDuration, ok := op.Inputs()["duration"].(string)
	if !ok {
		return errors.New("Missing duration input")
	}

	duration, err := time.ParseDuration(inputDuration)
	if err != nil {
		return fmt.Errorf("Invalid duration: %w", err)
	}

	logger.Warnf("Starting wait handler operation for %s", duration.String())

	// Initialize metadata map if needed.
	if op.Metadata() == nil {
		err = op.UpdateMetadata(make(map[string]any))
		if err != nil {
			return fmt.Errorf("Failed initializing operation metadata: %w", err)
		}
	}

	// See if some waiting was already done.
	elapsed := time.Duration(0)
	elapsedMetadata, ok := op.Metadata()["elapsed"]
	if ok {
		elapsed, err = time.ParseDuration(elapsedMetadata.(string))
		if err != nil {
			return fmt.Errorf("Failed parsing elapsed metadata: %w", err)
		}

		logger.Warnf("Resuming wait handler operation, already waited for %s", elapsed.String())
	}

	for duration > elapsed {
		// Sleep for one second, or until the run context is cancelled.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.Tick(time.Second):
		}

		elapsed = elapsed + time.Second
		logger.Warnf("Wait handler operation running for %d seconds...", elapsed/time.Second)
		op.Metadata()["elapsed"] = elapsed.String()
		err = op.UpdateMetadata(op.Metadata())
		if err != nil {
			return fmt.Errorf("Failed updating operation metadata: %w", err)
		}

		err = op.CommitMetadata()
		if err != nil {
			return fmt.Errorf("Failed committing operation metadata: %w", err)
		}
	}

	logger.Warn("Wait handler operation completed")

	return nil
}

// operationWaitHandler creates a dummy operation that waits for a specified duration.
func operationWaitHandler(d *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	// Extract the entity URL and duration from the request.
	req := operationWaitPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Parse the duration.
	duration, err := time.ParseDuration(req.Duration)
	if err != nil {
		return response.BadRequest(err)
	}

	// Extract and validate resources
	var resources map[string][]api.URL
	if req.Resources != nil {
		resources = make(map[string][]api.URL)
		for resourceType, entityURLs := range req.Resources {
			for _, entityURL := range entityURLs {
				parsedURL, err := url.Parse(entityURL)
				if err != nil {
					return response.BadRequest(err)
				}

				_, _, _, _, err = entity.ParseURL(*parsedURL)
				if err != nil {
					return response.BadRequest(err)
				}

				resources[resourceType] = append(resources[resourceType], api.URL{URL: *parsedURL})
			}
		}
	}

	err = operationtype.Validate(req.OpType)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid operation type code %d", req.OpType))
	}

	inputs := map[string]any{
		"duration": duration.String(),
	}

	var onConnect func(op *operations.Operation, r *http.Request, w http.ResponseWriter) error
	if req.OpClass == operations.OperationClassWebsocket {
		onConnect = func(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
			// Do nothing
			return nil
		}
	}

	args := operations.OperationArgs{
		ProjectName: request.QueryParam(r, "project"),
		Type:        req.OpType,
		Class:       req.OpClass,
		Resources:   resources,
		RunHook:     waitHandlerOperationRunHook,
		ConnectHook: onConnect,
		Inputs:      inputs,
	}

	// Durable operations have their run hook set in the DurableOperations table.
	if req.OpClass == operations.OperationClassDurable {
		args.RunHook = nil
	}

	op, err := operations.CreateUserOperation(d.State(), requestor, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
