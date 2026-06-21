package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
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
	"github.com/canonical/lxd/shared/version"
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

// runningInstanceOperations returns a map of project name to map of instance name to list of running operations.
// This is used to determine if an instance is busy and should not be shut down immediately.
func runningInstanceOperations() map[string]map[string][]*operations.Operation {
	res := make(map[string]map[string][]*operations.Operation)

	// function to parse a URL into project an instance name and set the operation if not already present in the map for
	// that instance.
	setInstanceOp := func(u url.URL, op *operations.Operation) {
		_, project, _, pathParts, err := entity.ParseURL(u)
		if err != nil || len(pathParts) != 1 {
			logger.Error("Failed parsing operation entity or resource URL during shutdown", logger.Ctx{"err": err, "url": u})
			return
		}

		_, ok := res[project]
		if !ok {
			res[project] = map[string][]*operations.Operation{
				pathParts[0]: {op},
			}

			return
		}

		alreadySet := slices.ContainsFunc(res[project][pathParts[0]], func(operation *operations.Operation) bool {
			return op.ID() == operation.ID()
		})

		if alreadySet {
			return
		}

		res[project][pathParts[0]] = append(res[project][pathParts[0]], op)
	}

	// Collect all running operations that reference an instance.
	// A single operation may reference multiple instances via resources (e.g. bulk state update).
	// A single instance may be referenced by multiple operations (e.g. multiple exec websockets).
	ops := operations.Clone()
	for _, op := range ops {
		if !op.IsRunning() || op.Class() == operations.OperationClassToken {
			continue
		}

		if op.Type().EntityType() == entity.TypeInstance {
			setInstanceOp(op.EntityURL().URL, op)
		}

		resources := op.Resources()
		if resources == nil {
			continue
		}

		for _, instanceURL := range resources[entity.TypeInstance] {
			setInstanceOp(instanceURL.URL, op)
		}
	}

	return res
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

	id := r.PathValue("id")
	var err error
	recursion, _ := util.IsRecursionRequest(r)

	// Load the operation from the database.
	var op *operations.Operation
	var childCount int64
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbOp, err := dbCluster.GetOperation(ctx, tx.Tx(), id)
		if err != nil {
			return err
		}

		// Don't return child operations directly.
		// Child operations can be returned embedded in their parents with recursion=1.
		if dbOp.Row.Parent != nil {
			return api.StatusErrorf(http.StatusBadRequest, "Child operations cannot be retrieved individually")
		}

		op, err = operations.ConstructOperationFromDB(ctx, tx.Tx(), s, dbOp)
		if err != nil {
			return err
		}

		if recursion > 0 {
			// Load all child operations for embedding in the response.
			childDbOps, err := dbCluster.GetOperationsWithParent(ctx, tx.Tx(), dbOp.Row.ID)
			if err != nil {
				return err
			}

			children := make([]*operations.Operation, 0, len(childDbOps))
			for _, childDbOp := range childDbOps {
				childOp, err := operations.ConstructOperationFromDB(ctx, tx.Tx(), s, &childDbOp)
				if err != nil {
					return err
				}

				children = append(children, childOp)
			}

			op.AddChildren(children...)
			childCount = int64(len(children))
		} else {
			// Count children from DB without loading full child operations.
			childCount, err = dbCluster.CountOperationChildren(ctx, tx.Tx(), dbOp.Row.ID)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = checkOperationViewAccess(r.Context(), op, s.Authorizer, "")
	if err != nil {
		return response.SmartError(err)
	}

	_, body := op.RenderFullWithoutProgress()
	body.ChildCount = childCount

	return response.SyncResponse(true, body)
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

	id := r.PathValue("id")
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
	var operation *dbCluster.Operation
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		operation, err = dbCluster.GetOperation(ctx, tx.Tx(), id)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Don't forward the request if we don't have where to forward it to.
	if operation.NodeAddress == "" || operation.NodeAddress == s.LocalConfig.ClusterAddress() {
		if api.StatusCode(operation.Row.StatusCode).IsFinal() {
			return response.BadRequest(errors.New("Operation already finalized"))
		}

		return response.SmartError(fmt.Errorf("Operation ID %q is not running on this member", id))
	}

	client, err := cluster.Connect(r.Context(), operation.NodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client)
}

// operationCancelToken cancels a token operation that exists on any member.
func operationCancelToken(ctx context.Context, s *state.State, projectName string, op *api.Operation) error {
	if op.Class != api.OperationClassToken {
		return fmt.Errorf("Expected operation of class %q but received %q", api.OperationClassToken, op.Class)
	}

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
		operation, err := dbCluster.GetOperation(ctx, tx.Tx(), op.ID)
		if err != nil {
			return err
		}

		memberAddress = operation.NodeAddress
		return nil
	})
	if err != nil {
		return err
	}

	// When cancelling a token operation we need to pass in a context that DOES NOT contain a requestor.
	// Tokens are used by untrusted callers for temporary access to LXD to specific endpoints.
	// The caller does not have permission to actually cancel the operation.
	// In the case, the cluster is cancelling its own operation because it received a valid token.
	client, err := cluster.Connect(s.ShutdownCtx, memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), true)
	if err != nil {
		return fmt.Errorf("Failed connecting to %q: %w", memberAddress, err)
	}

	err = client.UseProject(projectName).DeleteOperation(op.ID)
	if err != nil {
		return fmt.Errorf("Failed deleting remote operation %q on %q: %w", op.ID, memberAddress, err)
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

	canViewProjectOperations, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewOperations, entity.TypeProject)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed getting operation permission checker: %w", err))
	}

	// Not all operations have a project. Operations that don't have a project should be considered "server level".
	var canViewServerOperations bool
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanViewOperations)
	if err == nil {
		canViewServerOperations = true
	} else if !auth.IsDeniedError(err) {
		return response.SmartError(fmt.Errorf("Failed checking caller access to server operations: %w", err))
	}

	recursion, _ := util.IsRecursionRequest(r)

	// Map of parent operations keyed by the operation ID.
	parentOps := make(map[int64]*operations.Operation)

	// Child counts by parent UUID, populated via aggregate DB query for recursion < 2.
	childCounts := make(map[string]int64)

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := dbCluster.GetProjectIDsToNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading project IDs to names: %w", err)
		}

		// Make sure the requested project exists if not all-projects.
		if !allProjects {
			found := false
			for _, name := range projects {
				if name == projectName {
					found = true
					break
				}
			}

			if !found {
				return fmt.Errorf("Project %q does not exist", projectName)
			}
		}

		// For non-recursive responses, retrieve child counts via an aggregate query rather than
		// loading and constructing every child operation just to compute counts.
		if recursion < 2 {
			childCounts, err = dbCluster.CountOperationChildrenByParent(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed getting child operation counts: %w", err)
			}
		}

		// For non-recursive responses, only load parent operations from the DB.
		// For recursive responses, load all operations so children can be embedded.
		whereClause := ""
		if recursion < 2 {
			whereClause = "WHERE operations.parent IS NULL"
		}

		dbOps, err := query.Select[dbCluster.Operation](ctx, tx.Tx(), whereClause)
		if err != nil {
			return fmt.Errorf("Failed getting operations: %w", err)
		}

		// Map of child operations keyed by their parent operation ID (only used for recursion >= 2).
		childOps := make(map[int64][]*operations.Operation)
		for _, dbOp := range dbOps {
			// Get operation project name if it has one.
			operationProject := ""
			if dbOp.Row.ProjectID != nil {
				var ok bool
				operationProject, ok = projects[*dbOp.Row.ProjectID]
				if !ok {
					return fmt.Errorf("Failed finding project name for operation with non-existent project ID %d", *dbOp.Row.ProjectID)
				}
			}

			if !allProjects && operationProject != "" && operationProject != projectName {
				continue
			}

			// Omit operations that don't have a project if the caller does not have access to server operations.
			if operationProject == "" && !canViewServerOperations {
				continue
			}

			// Construct the operation object, which will also reconstruct its requestor.
			op, err := operations.ConstructOperationFromDB(ctx, tx.Tx(), s, &dbOp)
			if err != nil {
				return fmt.Errorf("Failed loading operation ID %q: %w", dbOp.Row.UUID, err)
			}

			// Omit operations if the caller does not have `can_view_operations` on the operations' project and the caller is not the operation owner.
			if !canViewProjectOperations(entity.ProjectURL(operationProject)) && !requestor.CallerIsEqual(op.Requestor()) {
				continue
			}

			// If this is a child operation, add it to the list keyed by parent DB ID.
			// We'll match these to actual parents later. This only occurs for recursion >= 2
			// since child operations are excluded from the DB query for recursion < 2.
			if dbOp.Row.Parent != nil {
				_, ok := childOps[*dbOp.Row.Parent]
				if !ok {
					childOps[*dbOp.Row.Parent] = make([]*operations.Operation, 0)
				}

				childOps[*dbOp.Row.Parent] = append(childOps[*dbOp.Row.Parent], op)
			} else {
				parentOps[dbOp.Row.ID] = op
			}
		}

		// Now add the child operations to their parents (only for recursion >= 2).
		for parentID, children := range childOps {
			parentOp, ok := parentOps[parentID]
			if !ok {
				logger.Warn("Failed finding parent operation for child operations, skipping children", logger.Ctx{"parentID": parentID})
				continue
			}

			parentOp.AddChildren(children...)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Render the operations.
	apiOps := make([]*api.OperationFull, 0, len(parentOps))
	for _, op := range parentOps {
		var apiOp *api.OperationFull
		if recursion >= 2 {
			_, apiOp = op.RenderFullWithoutProgress()
		} else {
			_, retOp := op.RenderWithoutProgress()
			retOp.ChildCount = childCounts[op.ID()]
			apiOp = &api.OperationFull{Operation: *retOp}
		}

		apiOps = append(apiOps, apiOp)
	}

	// Sort operations by UUID. Since we use UUIDv7, this will also sort operations by creation time.
	slices.SortFunc(apiOps, func(a, b *api.OperationFull) int {
		return strings.Compare(a.ID, b.ID)
	})

	// Sort all operations per status.
	md := map[string]any{}
	for _, apiOp := range apiOps {
		status := strings.ToLower(apiOp.Status)

		_, ok := md[status]
		if !ok {
			if recursion == 0 {
				md[status] = make([]string, 0)
			} else {
				md[status] = make([]*api.OperationFull, 0)
			}
		}

		if recursion == 0 {
			md[status] = append(md[status].([]string), api.NewURL().Path(version.APIVersion, "operations", apiOp.ID).String())
		} else {
			md[status] = append(md[status].([]*api.OperationFull), apiOp)
		}
	}

	return response.SyncResponse(true, md)
}

// operationsGetByType gets all operations for a project and type.
// It does not populate operation resources.
func operationsGetByType(ctx context.Context, s *state.State, projectName string, opType operationtype.Type, excludeOffline bool) ([]*api.Operation, error) {
	// Get all operations of the specified type in project.
	var ops []dbCluster.Operation
	var members []db.NodeInfo
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		if s.ServerClustered && excludeOffline {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return err
			}
		}

		ops, err = dbCluster.GetOperationsByProjectAndType(ctx, tx.Tx(), projectName, opType)
		if err != nil {
			return fmt.Errorf("Failed getting operations for project %q and type %d: %w", projectName, opType, err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Map of online members. online[op.NodeAddress] is only true if the address exists and is online
	// (if the address doesn't exist, the zero value for bool is false).
	online := make(map[string]bool, len(members))
	if excludeOffline {
		offlineThreshold := s.GlobalConfig.OfflineThreshold()
		for _, member := range members {
			online[member.Address] = !member.IsOffline(offlineThreshold)
		}
	}

	apiOps := make([]*api.Operation, 0, len(ops))
	for _, op := range ops {
		if s.ServerClustered && excludeOffline && !online[op.NodeAddress] {
			continue
		}

		var metadata map[string]any
		err := json.Unmarshal([]byte(op.Row.Metadata), &metadata)
		if err != nil {
			return nil, fmt.Errorf("Failed reading operation metadata: %w", err)
		}

		var requestor *api.OperationRequestor
		if op.Row.RequestorProtocol != nil {
			requestor = &api.OperationRequestor{
				Username: op.IdentityIdentifier,
				Protocol: string(*op.Row.RequestorProtocol),
			}
		}

		apiOps = append(apiOps, &api.Operation{
			ID:          op.Row.UUID,
			Class:       operations.OperationClass(op.Row.Class).String(),
			Description: op.Row.Type.Description(),
			CreatedAt:   op.Row.CreatedAt,
			UpdatedAt:   op.Row.UpdatedAt,
			Status:      api.StatusCode(op.Row.StatusCode).String(),
			StatusCode:  api.StatusCode(op.Row.StatusCode),
			Metadata:    metadata,
			MayCancel:   true,
			Err:         op.Row.Error,
			Location:    op.NodeName,
			Requestor:   requestor,
		})
	}

	return apiOps, nil
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

	id := r.PathValue("id")
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

			_, body := op.Render()
			_ = response.SyncResponse(true, body).Render(w, r)
			return nil
		}

		return response.ManualResponse(waitResponse)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		operation, err := dbCluster.GetOperation(ctx, tx.Tx(), id)
		if err != nil {
			return err
		}

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

	id := r.PathValue("id")
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
		operation, err := dbCluster.GetOperation(ctx, tx.Tx(), id)
		if err != nil {
			return err
		}

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
			logger.Error("Failed getting leader cluster member address", logger.Ctx{"err": err})
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

		op, err := operations.ScheduleServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating remove orphaned operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed removing orphaned operations", logger.Ctx{"err": err})
			return
		}
	}

	// All the cluster tasks are starting at the daemon init, at which time the cluster heartbeats
	// have not yet been updated. [cluster.RemoveOrphanedOperations] might start deleting operations
	// which are just starting on other nodes. To avoid this, we remove orphaned operations both in this
	// task (only runs after an hour of uptime) and after an initial heartbeat round (see [(*Daemon).nodeRefreshTask]).
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

		offlineMembers := make([]int64, 0, len(members))
		for _, member := range members {
			// Skip online nodes
			if !member.IsOffline(offlineThreshold) {
				continue
			}

			offlineMembers = append(offlineMembers, member.ID)
		}

		err = dbCluster.ClearStaleOperationsFromNodes(ctx, tx.Tx(), offlineMembers...)
		if err != nil {
			return fmt.Errorf("Failed deleting operations from offline members: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed removing orphaned operations: %w", err)
	}

	logger.Debug("Done removing orphaned operations across the cluster")

	return nil
}

// PruneExpiredOperationsTask returns a task function and schedule that is used to prune expired operations from the database.
func pruneExpiredOperationsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		leaderInfo, err := s.LeaderInfo()
		if err != nil {
			logger.Error("Failed getting leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if !leaderInfo.Leader {
			logger.Debug("Skipping pruning expired operations since we're not leader")
			return
		}

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return operations.PruneExpiredOperations(ctx, s)
		}

		args := operations.OperationArgs{
			Type:    operationtype.PruneExpiredOperations,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		}

		op, err := operations.ScheduleServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating prune expired operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed pruning expired operations", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Hourly()
}

// operationWaitPost represents the fields of a request to register a dummy operation.
type operationWaitPost struct {
	Duration          string                    `json:"duration" yaml:"duration"`
	OpClass           operations.OperationClass `json:"op_class" yaml:"op_class"`
	OpType            operationtype.Type        `json:"op_type" yaml:"op_type"`
	EntityURL         string                    `json:"entity_url" yaml:"entity_url"`
	ConflictReference string                    `json:"conflict_reference" yaml:"conflict_reference"`
}

// operationWaitHandler creates a dummy operation that waits for a specified duration.
func operationWaitHandler(d *Daemon, r *http.Request) response.Response {
	// Extract the entity URL and duration from the request.
	req := operationWaitPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Parse the duration.
	duration, err := time.ParseDuration(req.Duration)
	if err != nil {
		return response.BadRequest(err)
	}

	err = operationtype.Validate(req.OpType)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid operation type code %d", req.OpType))
	}

	run := func(ctx context.Context, op *operations.Operation) error {
		// Sleep for the duration, or until the run context is cancelled.
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}

		return nil
	}

	u, err := url.Parse(req.EntityURL)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed parsing operation entity URL: %w", err))
	}

	var onConnect func(op *operations.Operation, r *http.Request, w http.ResponseWriter) error
	if req.OpClass == operations.OperationClassWebsocket {
		onConnect = func(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
			// Do nothing
			return nil
		}
	}

	args := operations.OperationArgs{
		ProjectName:       request.QueryParam(r, "project"),
		Type:              req.OpType,
		Class:             req.OpClass,
		RunHook:           run,
		ConnectHook:       onConnect,
		EntityURL:         &api.URL{URL: *u},
		ConflictReference: req.ConflictReference,
	}

	op, err := operations.ScheduleServerOperation(d.State(), args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
