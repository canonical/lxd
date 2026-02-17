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

// runningInstanceOperations returns a map of project name to map of instance name to list of running operations.
// This is used to determine if an instance is busy and should not be shut down immediately.
func runningInstanceOperations() map[string]map[string][]*operations.Operation {
	res := make(map[string]map[string][]*operations.Operation)

	// function to parse a URL into project an instance name and set the operation if not already present in the map for
	// that instance.
	setInstanceOp := func(u url.URL, op *operations.Operation) {
		_, project, _, pathParts, err := entity.ParseURL(u)
		if err != nil || len(pathParts) != 1 {
			logger.Error("Failed to parse operation entity or resource URL during shutdown", logger.Ctx{"err": err, "url": u})
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

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		err := checkOperationViewAccess(r.Context(), op, s.Authorizer, "")
		if err != nil {
			return response.SmartError(err)
		}

		_, body, err = op.Render()
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, body)
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

	if !allProjects && projectName != api.ProjectDefaultName {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := dbCluster.GetProjectID(ctx, tx.Tx(), projectName)
			if err != nil {
				return fmt.Errorf("Failed to get project: %w", err)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
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

	localOperationURLs := func() (shared.Jmap, error) {
		// Get all the operations.
		localOps := operations.Clone()

		// Build a list of URLs.
		body := shared.Jmap{}

		for _, v := range localOps {
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

	recursion, _ := util.IsRecursionRequest(r)

	// Check if called from a cluster node.
	if requestor.IsClusterNotification() {
		// Only return the local data.
		if recursion > 0 {
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

	if recursion > 0 {
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

	// If not clustered, then just return local operations.
	if !s.ServerClustered {
		return response.SyncResponse(true, md)
	}

	// Get all nodes with running operations in this project.
	var membersWithOps []string
	var members []db.NodeInfo
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		if allProjects {
			membersWithOps, err = tx.GetAllNodesWithOperations(ctx)
		} else {
			membersWithOps, err = tx.GetNodesWithOperations(ctx, projectName)
		}

		if err != nil {
			return fmt.Errorf("Failed getting members with operations: %w", err)
		}

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get local address.
	localClusterAddress := s.LocalConfig.ClusterAddress()
	offlineThreshold := s.GlobalConfig.OfflineThreshold()

	memberOnline := func(memberAddress string) bool {
		for _, member := range members {
			if member.Address == memberAddress {
				if member.IsOffline(offlineThreshold) {
					logger.Warn("Excluding offline member from operations list", logger.Ctx{"member": member.Name, "address": member.Address, "ID": member.ID, "lastHeartbeat": member.Heartbeat})
					return false
				}

				return true
			}
		}

		return false
	}

	networkCert := s.Endpoints.NetworkCert()
	for _, memberAddress := range membersWithOps {
		if memberAddress == localClusterAddress {
			continue
		}

		if !memberOnline(memberAddress) {
			continue
		}

		// Connect to the remote server. Use notify=true to only get local operations on remote member.
		client, err := cluster.Connect(r.Context(), memberAddress, networkCert, s.ServerCert(), true)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed connecting to member %q: %w", memberAddress, err))
		}

		// Get operation data.
		var ops []api.Operation
		if allProjects {
			ops, err = client.GetOperationsAllProjects()
		} else {
			ops, err = client.UseProject(projectName).GetOperations()
		}

		if err != nil {
			logger.Warn("Failed getting operations from member", logger.Ctx{"address": memberAddress, "err": err})
			continue
		}

		// Merge with existing data.
		for _, op := range ops {
			status := strings.ToLower(op.Status)

			_, ok := md[status]
			if !ok {
				if recursion > 0 {
					md[status] = make([]*api.Operation, 0)
				} else {
					md[status] = make([]string, 0)
				}
			}

			if recursion > 0 {
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

		op, err := operations.ScheduleServerOperation(s, args)
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

			err = dbCluster.DeleteOperations(ctx, tx.Tx(), member.ID)
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

// operationWaitPost represents the fields of a request to register a dummy operation.
type operationWaitPost struct {
	Duration  string                    `json:"duration" yaml:"duration"`
	OpClass   operations.OperationClass `json:"op_class" yaml:"op_class"`
	OpType    operationtype.Type        `json:"op_type" yaml:"op_type"`
	EntityURL string                    `json:"entity_url" yaml:"entity_url"`
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.Tick(duration):
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
		ProjectName: request.QueryParam(r, "project"),
		Type:        req.OpType,
		Class:       req.OpClass,
		RunHook:     run,
		ConnectHook: onConnect,
		EntityURL:   &api.URL{URL: *u},
	}

	op, err := operations.ScheduleServerOperation(d.State(), args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
