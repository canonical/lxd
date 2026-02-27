package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/placement"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

type evacuateStopFunc func(inst instance.Instance) error
type evacuateMigrateFunc func(ctx context.Context, s *state.State, inst instance.Instance, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, op *operations.Operation) error

type evacuateOpts struct {
	s               *state.State
	gateway         *cluster.Gateway
	instances       []instance.Instance
	mode            string
	srcMemberName   string
	stopInstance    evacuateStopFunc
	migrateInstance evacuateMigrateFunc
	op              *operations.Operation
}

var clusterMembersCmd = APIEndpoint{
	Path:        "cluster/members",
	MetricsType: entity.TypeClusterMember,

	Get:  APIEndpointAction{Handler: clusterMembersGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: clusterMembersPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var clusterMemberCmd = APIEndpoint{
	Path:        "cluster/members/{name}",
	MetricsType: entity.TypeClusterMember,

	Delete: APIEndpointAction{Handler: clusterMemberDelete, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Get:    APIEndpointAction{Handler: clusterMemberGet, AccessHandler: allowAuthenticated},
	Patch:  APIEndpointAction{Handler: clusterMemberPatch, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: clusterMemberPut, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: clusterMemberPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var clusterMemberStateCmd = APIEndpoint{
	Path:        "cluster/members/{name}/state",
	MetricsType: entity.TypeClusterMember,

	Get:  APIEndpointAction{Handler: clusterMemberStateGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: clusterMemberStatePost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation GET /1.0/cluster/members cluster cluster_members_get
//
//  Get the cluster members
//
//  Returns a list of cluster members (URLs).
//
//  ---
//  produces:
//    - application/json
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
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/cluster/members/lxd01",
//                "/1.0/cluster/members/lxd02"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/members?recursion=1 cluster cluster_members_get_recursion1
//
//	Get the cluster members
//
//	Returns a list of cluster members (structs).
//
//	---
//	produces:
//	  - application/json
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
//	          description: List of cluster members
//	          items:
//	            $ref: "#/definitions/ClusterMember"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMembersGet(d *Daemon, r *http.Request) response.Response {
	recursion, _ := util.IsRecursionRequest(r)
	s := d.State()

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return response.InternalError(err)
	}

	if !leaderInfo.Clustered {
		return response.InternalError(cluster.ErrNodeIsNotClustered)
	}

	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var members []db.NodeInfo
	var membersInfo []api.ClusterMember
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		if recursion > 0 {
			memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
			if err != nil {
				return fmt.Errorf("Failed loading member failure domains: %w", err)
			}

			args := db.NodeInfoArgs{
				LeaderAddress:        leaderInfo.Address,
				FailureDomains:       failureDomains,
				MemberFailureDomains: memberFailureDomains,
				OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
				Members:              members,
				RaftNodes:            raftNodes,
			}

			membersInfo = make([]api.ClusterMember, 0, len(members))
			for i := range members {
				member, err := members[i].ToAPI(ctx, tx, args)
				if err != nil {
					return err
				}

				membersInfo = append(membersInfo, *member)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion > 0 {
		return response.SyncResponse(true, membersInfo)
	}

	urls := make([]string, 0, len(members))
	for _, member := range members {
		u := api.NewURL().Path(version.APIVersion, "cluster", "members", member.Name)
		urls = append(urls, u.String())
	}

	return response.SyncResponse(true, urls)
}

var clusterMembersPostMu sync.Mutex // Used to prevent races when creating cluster join tokens.

// swagger:operation POST /1.0/cluster/members cluster cluster_members_post
//
//	Request a join token
//
//	Requests a join token to add a cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member add request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMembersPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMembersPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ClusterMembersPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	if req.ServerName == "none" {
		return response.BadRequest(fmt.Errorf("Join token name cannot be %q", req.ServerName))
	}

	expiry, err := shared.GetExpiry(time.Now(), s.GlobalConfig.ClusterJoinTokenExpiry())
	if err != nil {
		return response.BadRequest(err)
	}

	// Get target addresses for existing online members, so that it can be encoded into the join token so that
	// the joining member will not have to specify a joining address during the join process.
	// Use anonymous interface type to align with how the API response will be returned for consistency when
	// retrieving remote operations.
	onlineNodeAddresses := make([]any, 0)

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the nodes.
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		// Filter to online members.
		onlineNodeAddresses = make([]any, 0, len(members))
		for _, member := range members {
			if member.State == db.ClusterMemberStateEvacuated || member.IsOffline(s.GlobalConfig.OfflineThreshold()) {
				continue
			}

			onlineNodeAddresses = append(onlineNodeAddresses, member.Address)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(onlineNodeAddresses) < 1 {
		return response.InternalError(errors.New("There are no online cluster members"))
	}

	// Lock to prevent concurrent requests racing the operationsGetByType function and creating duplicates.
	// We have to do this because collecting all of the operations from existing cluster members can take time.
	clusterMembersPostMu.Lock()
	defer clusterMembersPostMu.Unlock()

	// Remove any existing join tokens for the requested cluster member, this way we only ever have one active
	// join token for each potential new member, and it has the most recent active members list for joining.
	// This also ensures any historically unused (but potentially published) join tokens are removed.
	ops, err := operationsGetByType(r.Context(), s, "", operationtype.ClusterJoinToken)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed getting cluster join token operations: %w", err))
	}

	for _, op := range ops {
		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		opServerName, ok := op.Metadata["serverName"]
		if !ok {
			continue
		}

		if opServerName == req.ServerName {
			// Join token operation matches requested server name, so lets cancel it.
			logger.Warn("Cancelling duplicate join token operation", logger.Ctx{"operation": op.ID, "serverName": opServerName})
			err = operationCancel(r.Context(), s, "", op)
			if err != nil {
				return response.InternalError(fmt.Errorf("Failed cancelling operation %q: %w", op.ID, err))
			}
		}
	}

	// Generate join secret for new member. This will be stored inside the join token operation and will be
	// supplied by the joining member (encoded inside the join token) which will allow us to lookup the correct
	// operation in order to validate the requested joining server name is correct and authorised.
	joinSecret, err := shared.RandomCryptoString()
	if err != nil {
		return response.InternalError(err)
	}

	// Generate fingerprint of network certificate so joining member can automatically trust the correct
	// certificate when it is presented during the join process.
	fingerprint, err := shared.CertFingerprintStr(string(s.Endpoints.NetworkPublicKey()))
	if err != nil {
		return response.InternalError(err)
	}

	meta := map[string]any{
		"serverName":  req.ServerName, // Add server name to allow validation of name during join process.
		"secret":      joinSecret,
		"fingerprint": fingerprint,
		"addresses":   onlineNodeAddresses,
		"expiresAt":   expiry,
	}

	args := operations.OperationArgs{
		Type:     operationtype.ClusterJoinToken,
		Class:    operations.OperationClassToken,
		Metadata: meta,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterTokenCreated.Event("members", op.EventLifecycleRequestor(), nil))

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/cluster/members/{name} cluster cluster_member_get
//
//	Get the cluster member
//
//	Gets a specific cluster member.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster member
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
//	          $ref: "#/definitions/ClusterMember"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMemberGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return response.InternalError(err)
	}

	if !leaderInfo.Clustered {
		return response.InternalError(cluster.ErrNodeIsNotClustered)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var memberInfo *api.ClusterMember
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading member failure domains: %w", err)
		}

		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		var member db.NodeInfo
		for _, m := range members {
			if m.Name == name {
				member = m
				break
			}
		}

		if member.ID == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Cluster member not found %v", member)
		}

		args := db.NodeInfoArgs{
			LeaderAddress:        leaderInfo.Address,
			FailureDomains:       failureDomains,
			MemberFailureDomains: memberFailureDomains,
			OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
			Members:              members,
			RaftNodes:            raftNodes,
		}

		memberInfo, err = member.ToAPI(ctx, tx, args)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, memberInfo, memberInfo.Writable())
}

// swagger:operation PATCH /1.0/cluster/members/{name} cluster cluster_member_patch
//
//	Partially update the cluster member
//
//	Updates a subset of the cluster member configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMemberPatch(d *Daemon, r *http.Request) response.Response {
	return updateClusterMember(d.State(), d.gateway, r, true)
}

// swagger:operation PUT /1.0/cluster/members/{name} cluster cluster_member_put
//
//	Update the cluster member
//
//	Updates the entire cluster member configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMemberPut(d *Daemon, r *http.Request) response.Response {
	return updateClusterMember(d.State(), d.gateway, r, false)
}

// updateClusterMember is shared between clusterMemberPut and clusterMemberPatch.
func updateClusterMember(s *state.State, gateway *cluster.Gateway, r *http.Request, isPatch bool) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseToNode(r.Context(), s, name)
	if resp != nil {
		return resp
	}

	leaderAddress, err := gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	var raftNodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading RAFT nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var member db.NodeInfo
	var memberInfo *api.ClusterMember
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		failureDomains, err := tx.GetFailureDomainsNames(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading failure domains names: %w", err)
		}

		memberFailureDomains, err := tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading member failure domains: %w", err)
		}

		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		for _, m := range members {
			if m.Name == name {
				member = m
				break
			}
		}

		if member.ID == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
		}

		args := db.NodeInfoArgs{
			LeaderAddress:        leaderAddress,
			FailureDomains:       failureDomains,
			MemberFailureDomains: memberFailureDomains,
			OfflineThreshold:     s.GlobalConfig.OfflineThreshold(),
			Members:              members,
			RaftNodes:            raftNodes,
		}

		memberInfo, err = member.ToAPI(ctx, tx, args)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the request is fine
	err = util.EtagCheck(r, memberInfo.Writable())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request
	req := api.ClusterMemberPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the request (preserves automatic roles)
	req.Roles, err = validateClusterMemberRoles(memberInfo.Roles, req.Roles)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate config before database transaction.
	err = clusterValidateConfig(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Nodes must belong to at least one group.
	if len(req.Groups) == 0 {
		return response.BadRequest(errors.New("Cluster members need to belong to at least one group"))
	}

	// Convert the roles.
	newRoles := make([]db.ClusterRole, 0, len(req.Roles))
	for _, role := range req.Roles {
		newRoles = append(newRoles, db.ClusterRole(role))
	}

	// Update the database
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		nodeInfo, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return fmt.Errorf("Loading node information: %w", err)
		}

		if isPatch {
			// Populate request config with current values.
			if req.Config == nil {
				req.Config = nodeInfo.Config
			} else {
				for k, v := range nodeInfo.Config {
					_, ok := req.Config[k]
					if !ok {
						req.Config[k] = v
					}
				}
			}
		}

		// Update node config.
		err = tx.UpdateNodeConfig(ctx, nodeInfo.ID, req.Config)
		if err != nil {
			return fmt.Errorf("Failed updating cluster member config: %w", err)
		}

		// Update the description.
		if req.Description != memberInfo.Description {
			err = tx.SetDescription(nodeInfo.ID, req.Description)
			if err != nil {
				return fmt.Errorf("Update description: %w", err)
			}
		}

		// Update the roles.
		err = tx.UpdateNodeRoles(nodeInfo.ID, newRoles)
		if err != nil {
			return fmt.Errorf("Update roles: %w", err)
		}

		err = tx.UpdateNodeFailureDomain(ctx, nodeInfo.ID, req.FailureDomain)
		if err != nil {
			return fmt.Errorf("Update failure domain: %w", err)
		}

		// Update the cluster groups.
		err = tx.UpdateNodeClusterGroups(ctx, nodeInfo.ID, req.Groups)
		if err != nil {
			return fmt.Errorf("Update cluster groups: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// If cluster roles changed, then distribute the info to all members.
	if s.Endpoints != nil && clusterRolesChanged(member.Roles, newRoles) {
		cluster.NotifyHeartbeat(s, gateway)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberUpdated.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// clusterRolesChanged checks whether manual roles have changed between oldRoles and newRoles.
func clusterRolesChanged(oldRoles []db.ClusterRole, newRoles []db.ClusterRole) bool {
	// Get manual roles to check against.
	manualRoles := slices.Collect(maps.Values(db.ClusterRoles[db.ClusterRoleClassManual]))

	// Filter roles to only manual (user-assignable) roles.
	newManualRoles := make([]db.ClusterRole, 0, len(newRoles))
	oldManualRoles := make([]db.ClusterRole, 0, len(oldRoles))

	for _, role := range newRoles {
		if slices.Contains(manualRoles, role) {
			newManualRoles = append(newManualRoles, role)
		}
	}

	for _, role := range oldRoles {
		if slices.Contains(manualRoles, role) {
			oldManualRoles = append(oldManualRoles, role)
		}
	}

	// Sort old and new roles for comparison.
	sortedOld := slices.Sorted(slices.Values(oldManualRoles))
	sortedNew := slices.Sorted(slices.Values(newManualRoles))

	return !slices.Equal(sortedOld, sortedNew)
}

// validateClusterMemberRoles validates and normalizes the requested roles for a cluster member update.
// It preserves any automatic roles not included in the request, then validates that:
// - Automatic roles cannot be added manually.
// - All requested roles are valid (exist in [db.ClusterRoles] map).
// - No duplicate roles are present in the request.
//
// Returns the normalized role list with automatic roles preserved.
func validateClusterMemberRoles(currentRoles []string, requestedRoles []string) ([]string, error) {
	// Used to check if a role is automatic.
	isAutomaticRole := func(role string) bool {
		return slices.Contains(slices.Collect(maps.Values(db.ClusterRoles[db.ClusterRoleClassAutomatic])), db.ClusterRole(role))
	}

	// Used to check if a role exists in either manual or automatic roles.
	isValidRole := func(role string) bool {
		for _, roles := range db.ClusterRoles {
			if slices.Contains(slices.Collect(maps.Values(roles)), db.ClusterRole(role)) {
				return true
			}
		}

		return false
	}

	// Preserve automatic roles not included in request.
	// This prevents removing automatic roles when updating cluster members.
	normalizedRoles := make([]string, len(requestedRoles))
	copy(normalizedRoles, requestedRoles)

	for _, currentRole := range currentRoles {
		if !slices.Contains(normalizedRoles, currentRole) && isAutomaticRole(currentRole) {
			normalizedRoles = append(normalizedRoles, currentRole)
		}
	}

	// Track seen roles to detect duplicates.
	seenRoles := make(map[string]bool, len(normalizedRoles))

	// Validate each role in the normalized list.
	for _, role := range normalizedRoles {
		// Check for duplicates
		if seenRoles[role] {
			return nil, fmt.Errorf("Duplicate role %q in request", role)
		}

		seenRoles[role] = true

		// Check if role exists and validate it.
		if !isValidRole(role) {
			return nil, fmt.Errorf("Invalid cluster role %q", role)
		}

		// Manual roles are always allowed.
		if !isAutomaticRole(role) {
			continue
		}

		// Automatic roles: can only be kept, not added.
		if !slices.Contains(currentRoles, role) {
			return nil, fmt.Errorf("The automatically assigned %q role cannot be added manually", role)
		}
	}

	return normalizedRoles, nil
}

// clusterValidateConfig validates the configuration keys/values for cluster members.
func clusterValidateConfig(config map[string]string) error {
	clusterConfigKeys := map[string]func(value string) error{
		// lxdmeta:generate(entities=cluster; group=cluster; key=scheduler.instance)
		// Possible values are `all`, `manual`, and `group`. See
		// {ref}`clustering-instance-placement` for more information.
		// ---
		//  type: string
		//  defaultdesc: `all`
		//  shortdesc: Controls how instances are scheduled to run on this member
		"scheduler.instance": validate.Optional(validate.IsOneOf("all", "group", "manual")),
	}

	for k, v := range config {
		// User keys are free for all.

		// lxdmeta:generate(entities=cluster; group=cluster; key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := clusterConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid cluster configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid cluster configuration key %q value", k)
		}
	}

	return nil
}

// swagger:operation POST /1.0/cluster/members/{name} cluster cluster_member_post
//
//	Rename the cluster member
//
//	Renames an existing cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMemberPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	memberName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Forward request.
	resp := forwardedResponseToNode(r.Context(), s, memberName)
	if resp != nil {
		return resp
	}

	req := api.ClusterMemberPost{}

	// Parse the request
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameNode(ctx, memberName, req.ServerName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Update local server name.
	d.globalConfigMu.Lock()
	d.serverName = req.ServerName
	d.globalConfigMu.Unlock()

	d.events.SetLocalLocation(d.serverName)

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberRenamed.Event(req.ServerName, requestor, logger.Ctx{"old_name": memberName}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/members/{name} cluster cluster_member_delete
//
//	Delete the cluster member
//
//	Removes the member from the cluster.
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
func clusterMemberDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Redirect all requests to the leader, which is the one with
	// knowledge of which nodes are part of the raft cluster.
	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return response.SmartError(err)
	}

	if !leaderInfo.Clustered {
		return response.InternalError(cluster.ErrNodeIsNotClustered)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	force := shared.IsTrue(r.FormValue("force"))

	localClusterAddress := s.LocalConfig.ClusterAddress()

	var localInfo, leaderNodeInfo db.NodeInfo
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		localInfo, err = tx.GetNodeByAddress(ctx, localClusterAddress)
		if err != nil {
			return fmt.Errorf("Failed loading local member info %q: %w", localClusterAddress, err)
		}

		leaderNodeInfo, err = tx.GetNodeByAddress(ctx, leaderInfo.Address)
		if err != nil {
			return fmt.Errorf("Failed loading leader member info %q: %w", leaderInfo.Address, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get information about the cluster.
	var nodes []db.RaftNode
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Unable to get raft nodes: %w", err))
	}

	if !leaderInfo.Leader {
		if localInfo.Name == name {
			// If the member being removed is ourselves and we are not the leader, then lock the
			// clusterPutDisableMu before we forward the request to the leader, so that when the leader
			// goes on to request clusterPutDisable back to ourselves it won't be actioned until we
			// have returned this request back to the original client.
			clusterPutDisableMu.Lock()
			logger.Info("Acquired cluster self removal lock", logger.Ctx{"member": localInfo.Name})

			go func() {
				<-r.Context().Done() // Wait until request is finished.

				logger.Info("Releasing cluster self removal lock", logger.Ctx{"member": localInfo.Name})
				clusterPutDisableMu.Unlock()
			}()
		}

		logger.Debug("Redirect member delete request", logger.Ctx{"leader": leaderInfo.Address})
		client, err := cluster.Connect(r.Context(), leaderInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return response.SmartError(err)
		}

		err = client.DeleteClusterMember(name, force)
		if err != nil {
			return response.SmartError(err)
		}

		// If we are the only remaining node, wait until promotion to leader,
		// then update cluster certs.
		if name == leaderNodeInfo.Name && len(nodes) == 2 {
			err = d.gateway.WaitLeadership()
			if err != nil {
				return response.SmartError(err)
			}

			s.UpdateIdentityCache()
		}

		return response.ManualResponse(func(w http.ResponseWriter) error {
			err := response.EmptySyncResponse.Render(w, r)
			if err != nil {
				return err
			}

			// Send the response before replacing the LXD daemon process.
			f, ok := w.(http.Flusher)
			if !ok {
				return errors.New("http.ResponseWriter is not type http.Flusher")
			}

			f.Flush()

			return nil
		})
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	// If we are removing the leader of a 2 node cluster, ensure the other node can be a leader.
	if name == leaderNodeInfo.Name && len(nodes) == 2 {
		for i := range nodes {
			if nodes[i].Address != leaderInfo.Address && nodes[i].Role != db.RaftVoter {
				// Promote the remaining node.
				nodes[i].Role = db.RaftVoter
				err := changeMemberRole(r.Context(), s, nodes[i].Address, nodes)
				if err != nil {
					return response.SmartError(fmt.Errorf("Unable to promote remaining cluster member to leader: %w", err))
				}

				break
			}
		}
	}

	logger.Info("Deleting member from cluster", logger.Ctx{"name": name, "force": force})

	err = autoSyncImages(s.ShutdownCtx, s)
	if err != nil {
		if !force {
			return response.SmartError(fmt.Errorf("Failed syncing images: %w", err))
		}

		// If force is set, only show a warning instead of returning an error.
		logger.Warn("Failed syncing images")
	}

	// First check that the node is clear from containers and images and
	// make it leave the database cluster, if it's part of it.
	address, err := cluster.Leave(s, d.gateway, name, force)
	if err != nil {
		return response.SmartError(err)
	}

	if !force {
		// Try to gracefully delete all networks and storage pools on it.
		// Delete all networks on this node
		client, err := cluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
		if err != nil {
			return response.SmartError(err)
		}

		// Get a list of projects for networks.
		var networkProjectNames []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkProjectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading projects for networks: %w", err))
		}

		for _, networkProjectName := range networkProjectNames {
			var networks []string

			err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				networks, err = tx.GetNetworks(ctx, networkProjectName)

				return err
			})
			if err != nil {
				return response.SmartError(err)
			}

			for _, name := range networks {
				err := client.UseProject(networkProjectName).DeleteNetwork(name)
				if err != nil {
					return response.SmartError(err)
				}
			}
		}

		var pools []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Delete all the pools on this node
			pools, err = tx.GetStoragePoolNames(ctx)

			return err
		})
		if err != nil && !response.IsNotFoundError(err) {
			return response.SmartError(err)
		}

		for _, name := range pools {
			err := client.DeleteStoragePool(name)
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	// Remove node from the database
	err = cluster.Purge(s.DB.Cluster, name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed removing member from database: %w", err))
	}

	err = rebalanceMemberRoles(r.Context(), s, d.gateway, nil)
	if err != nil {
		logger.Warnf("Failed rebalancing dqlite nodes: %v", err)
	}

	// If this leader node removed itself, just disable clustering.
	if address == localClusterAddress {
		return clusterPutDisable(d, r, api.ClusterPut{})
	} else if !force {
		// Try to gracefully reset the database on the node.
		client, err := cluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
		if err != nil {
			return response.SmartError(err)
		}

		put := api.ClusterPut{}
		put.Enabled = false
		_, err = client.UpdateCluster(put, "")
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed cleaning up the member: %w", err))
		}
	}

	// Refresh the trusted certificate cache now that the member certificate has been removed.
	// We do not need to notify the other members here because the next heartbeat will trigger member change
	// detection and updateIdentityCache is called as part of that.
	s.UpdateIdentityCache()

	// Ensure all images are available after this node has been deleted.
	err = autoSyncImages(s.ShutdownCtx, s)
	if err != nil {
		logger.Warn("Failed syncing images")
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberRemoved.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/cluster/members/{name}/state cluster cluster_member_state_get
//
//	Get state of the cluster member
//
//	Gets state of a specific cluster member.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster member state
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
//	          $ref: "#/definitions/ClusterMemberState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMemberStateGet(d *Daemon, r *http.Request) response.Response {
	memberName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	// Forward request.
	resp := forwardedResponseToNode(r.Context(), s, memberName)
	if resp != nil {
		return resp
	}

	memberState, err := cluster.MemberState(r.Context(), s)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, memberState)
}

// swagger:operation POST /1.0/cluster/members/{name}/state cluster cluster_member_state_post
//
//	Evacuate or restore a cluster member
//
//	Evacuates or restores a cluster member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster member state
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterMemberStatePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterMemberStatePost(d *Daemon, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	// Forward request
	resp := forwardedResponseToNode(r.Context(), s, name)
	if resp != nil {
		return resp
	}

	// Parse the request
	req := api.ClusterMemberStatePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Run some pre-checks before evacuating or restoring the cluster member.
	// It's important that those checks runs on the to be restored cluster member.
	if s.NetworkReady.Err() == nil {
		return response.BadRequest(fmt.Errorf("Cannot %s %q because some networks have not started yet", req.Action, d.serverName))
	} else if s.StorageReady.Err() == nil {
		return response.BadRequest(fmt.Errorf("Cannot %s %q because some storage pools have not started yet", req.Action, d.serverName))
	}

	switch req.Action {
	case "evacuate":
		ops, err := operationsGetByType(r.Context(), s, "", operationtype.ClusterMemberRestore)
		if err != nil {
			return response.SmartError(err)
		}

		for _, op := range ops {
			if op.Location == name && !op.StatusCode.IsFinal() {
				return response.BadRequest(fmt.Errorf("Cannot evacuate %q while a restore operation is in progress", name))
			}
		}

		stopFunc := func(inst instance.Instance) error {
			l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

			// Get the shutdown timeout for the instance.
			timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
			val, err := strconv.Atoi(timeout)
			if err != nil {
				val = evacuateHostShutdownDefaultTimeout
			}

			// Start with a clean shutdown.
			err = inst.Shutdown(time.Duration(val) * time.Second)
			if err != nil {
				l.Warn("Failed shutting down instance, forcing stop", logger.Ctx{"err": err})

				// Fallback to forced stop.
				err = inst.Stop(false)
				if err != nil && !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
					return fmt.Errorf("Failed stopping instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
				}
			}

			// Mark the instance as RUNNING in volatile so its state can be properly restored.
			err = inst.VolatileSet(map[string]string{"volatile.last_state.power": instance.PowerStateRunning})
			if err != nil {
				l.Warn("Failed setting instance state to RUNNING", logger.Ctx{"err": err})
			}

			return nil
		}

		migrateFunc := func(ctx context.Context, s *state.State, inst instance.Instance, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, op *operations.Operation) error {
			// Migrate the instance.
			req := api.InstancePost{
				Name: inst.Name(),
				Live: live,
			}

			err := migrateInstance(ctx, s, inst, targetMemberInfo.Name, "", req, nil, op)
			if err != nil {
				return fmt.Errorf("Failed migrating instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
			}

			if !startInstance || live {
				return nil
			}

			// Start it back up on target.
			dest, err := cluster.Connect(ctx, targetMemberInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
			if err != nil {
				return fmt.Errorf("Failed connecting to destination %q for instance %q in project %q: %w", targetMemberInfo.Address, inst.Name(), inst.Project().Name, err)
			}

			dest = dest.UseProject(inst.Project().Name)

			if op != nil {
				_ = op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)})
			}

			startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
			if err != nil {
				return err
			}

			err = startOp.Wait()
			if err != nil {
				return err
			}

			return nil
		}

		run := func(ctx context.Context, op *operations.Operation) error {
			return evacuateClusterMember(ctx, s, d.gateway, op, name, req.Mode, stopFunc, migrateFunc)
		}

		args := operations.OperationArgs{
			ProjectName: "",
			Type:        operationtype.ClusterMemberEvacuate,
			Class:       operations.OperationClassTask,
			RunHook:     run,
		}

		op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
		if err != nil {
			return response.SmartError(err)
		}

		return operations.OperationResponse(op)
	case "restore":
		ops, err := operationsGetByType(r.Context(), s, "", operationtype.ClusterMemberEvacuate)
		if err != nil {
			return response.SmartError(err)
		}

		for _, op := range ops {
			if op.Location == name && !op.StatusCode.IsFinal() {
				return response.BadRequest(fmt.Errorf("Cannot restore %q while an evacuate operation is in progress", name))
			}
		}

		return restoreClusterMember(d, r, req.Mode)
	}

	return response.BadRequest(fmt.Errorf("Unknown action %q", req.Action))
}

func evacuateClusterSetState(s *state.State, name string, state int) error {
	return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the node.
		node, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return fmt.Errorf("Failed getting cluster member by name: %w", err)
		}

		if node.State == db.ClusterMemberStatePending {
			return errors.New("Cannot evacuate or restore a pending cluster member")
		}

		// Do nothing if the node is already in expected state.
		if node.State == state {
			switch state {
			case db.ClusterMemberStateEvacuated:
				return errors.New("Cluster member is already evacuated")
			case db.ClusterMemberStateCreated:
				return errors.New("Cluster member is already restored")
			}

			return errors.New("Cluster member is already in requested state")
		}

		// Set node status to requested value.
		err = tx.UpdateNodeStatus(node.ID, state)
		if err != nil {
			return fmt.Errorf("Failed updating cluster member status: %w", err)
		}

		return nil
	})
}

// evacuateHostShutdownDefaultTimeout default timeout (in seconds) for waiting for clean shutdown to complete.
const evacuateHostShutdownDefaultTimeout = 30

func evacuateClusterMember(ctx context.Context, s *state.State, gateway *cluster.Gateway, op *operations.Operation, name string, mode string, stopInstance evacuateStopFunc, migrateInstance evacuateMigrateFunc) error {
	// The instances are retrieved in a separate transaction, after the node is in EVACUATED state.
	var dbInstances []dbCluster.Instance
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// If evacuating, consider only the instances on the node which needs to be evacuated.
		var err error
		dbInstances, err = dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Node: &name})
		if err != nil {
			return fmt.Errorf("Failed getting instances: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	instances := make([]instance.Instance, 0, len(dbInstances))

	for _, dbInst := range dbInstances {
		inst, err := instance.LoadByProjectAndName(s, dbInst.Project, dbInst.Name)
		if err != nil {
			return fmt.Errorf("Failed loading instance: %w", err)
		}

		instances = append(instances, inst)
	}

	// Setup a reverter.
	revert := revert.New()
	defer revert.Fail()

	// Set node status to EVACUATED.
	err = evacuateClusterSetState(s, name, db.ClusterMemberStateEvacuated)
	if err != nil {
		return err
	}

	// Ensure node is put into its previous state if anything fails.
	revert.Add(func() {
		_ = evacuateClusterSetState(s, name, db.ClusterMemberStateCreated)
	})

	opts := evacuateOpts{
		s:               s,
		gateway:         gateway,
		instances:       instances,
		mode:            mode,
		srcMemberName:   name,
		stopInstance:    stopInstance,
		migrateInstance: migrateInstance,
		op:              op,
	}

	err = evacuateInstances(context.Background(), opts)
	if err != nil {
		return err
	}

	// Evacuate networks too, but not during healing.
	if mode != api.ClusterEvacuateModeHeal {
		networkStop(s, true)
	}

	revert.Success()

	if mode != api.ClusterEvacuateModeHeal {
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterMemberEvacuated.Event(name, op.EventLifecycleRequestor(), nil))
	}

	return nil
}

func evacuateInstances(ctx context.Context, opts evacuateOpts) error {
	if opts.migrateInstance == nil {
		return errors.New("Missing migration callback function")
	}

	// Prepare a placement group cache to avoid reloading the same group repeatedly.
	pgCache := placement.NewCache()

	for _, inst := range opts.instances {
		instProject := inst.Project()
		l := logger.AddContext(logger.Ctx{"project": instProject.Name, "instance": inst.Name()})

		// Check if migratable.
		migrate, live := inst.CanMigrate()

		// Apply overrides.
		if opts.mode != "" {
			switch opts.mode {
			case api.ClusterEvacuateModeStop:
				migrate = false
				live = false
			case api.ClusterEvacuateModeMigrate:
				migrate = true
				live = false
			case api.ClusterEvacuateModeLiveMigrate:
				migrate = true
				live = true
			default:
				return fmt.Errorf("Invalid mode: %q", opts.mode)
			}
		}

		// Stop the instance if needed.
		isRunning := inst.IsRunning()
		if opts.stopInstance != nil && isRunning && (!migrate || !live) {
			_ = opts.op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Stopping %q in project %q", inst.Name(), instProject.Name)})

			err := opts.stopInstance(inst)
			if err != nil {
				return err
			}
		}

		// If not migratable, the instance is just stopped.
		if !migrate {
			continue
		}

		// Find a new location for the instance.
		targetMemberInfo, err := evacuateClusterSelectTarget(ctx, opts.s, inst, pgCache)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				// Skip migration if no target is available.
				l.Warn("No migration target available for instance")
				continue
			}

			return err
		}

		// Start migrating the instance.
		_ = opts.op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Migrating %q in project %q to %q", inst.Name(), instProject.Name, targetMemberInfo.Name)})

		// Set origin server (but skip if already set as that suggests more than one server being evacuated).
		if inst.LocalConfig()["volatile.evacuate.origin"] == "" {
			_ = inst.VolatileSet(map[string]string{"volatile.evacuate.origin": opts.srcMemberName})
		}

		start := isRunning || instanceShouldAutoStart(inst)
		err = opts.migrateInstance(ctx, opts.s, inst, targetMemberInfo, live, start, opts.op)
		if err != nil {
			return err
		}
	}

	return nil
}

func evacuateClusterSelectTarget(ctx context.Context, s *state.State, inst instance.Instance, pgCache *placement.Cache) (*db.NodeInfo, error) {
	var targetMemberInfo *db.NodeInfo
	var candidateMembers []db.NodeInfo

	// Get candidate cluster members to move instances to.
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		allMembers, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		// Placement groups and cluster group targets are mutually exclusive, with placement groups taking precedence.
		_, clusterGroupName := limits.TargetDetect(inst.LocalConfig()["volatile.cluster.group"])
		placementGroupName, ok := inst.ExpandedConfig()["placement.group"]

		instProject := inst.Project()
		clusterGroupsAllowed := limits.GetRestrictedClusterGroups(&instProject)

		// Filter offline servers.
		candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, "", clusterGroupsAllowed, s.GlobalConfig.OfflineThreshold())
		if err != nil {
			return err
		}

		if ok {
			// Filter candidates by placement group.
			placementGroup, err := pgCache.Get(ctx, tx, placementGroupName, inst.Project().Name)
			if err != nil {
				return err
			}

			apiPlacementGroup, err := placementGroup.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			filteredCandidates, err := placement.Filter(ctx, tx, candidateMembers, *apiPlacementGroup, true)
			if err != nil {
				// If no candidates remain due to placement constraints, signal not found so caller can skip instance during evacuation.
				if api.StatusErrorCheck(err, http.StatusConflict) {
					return api.StatusErrorf(http.StatusNotFound, "No eligible target cluster members after applying placement group %q", placementGroup.Name)
				}

				return err
			}

			// If placement group filtering returns candidates, use them.
			if len(filteredCandidates) > 0 {
				candidateMembers = filteredCandidates
			}
		} else if clusterGroupName != "" {
			// Filter candidates by cluster group.
			newMembers := make([]db.NodeInfo, 0, len(candidateMembers))
			for _, member := range candidateMembers {
				if !slices.Contains(member.Groups, clusterGroupName) {
					continue
				}

				newMembers = append(newMembers, member)
			}

			candidateMembers = newMembers
		}

		// Find the least loaded cluster member which supports the instance's architecture.
		targetMemberInfo, err = tx.GetNodeWithLeastInstances(ctx, candidateMembers)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return targetMemberInfo, nil
}

func restoreClusterMember(d *Daemon, r *http.Request, mode string) response.Response {
	s := d.State()

	originName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var instances []instance.Instance
	var localInstances []instance.Instance

	skipInstances := false
	if mode != "" {
		switch mode {
		case api.ClusterRestoreModeSkip:
			skipInstances = true
		default:
			return response.BadRequest(fmt.Errorf("Invalid mode: %q", mode))
		}
	}

	if !skipInstances {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			err = tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
				inst, err := instance.Load(s, dbInst, p)
				if err != nil {
					return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
				}

				if dbInst.Node == originName {
					localInstances = append(localInstances, inst)

					return nil
				}

				// Only consider instances where "volatile.evacuate.origin" is set to the node which needs to be restored.
				val, ok := inst.LocalConfig()["volatile.evacuate.origin"]
				if !ok || val != originName {
					return nil
				}

				instances = append(instances, inst)

				return nil
			})
			if err != nil {
				return fmt.Errorf("Failed getting instances: %w", err)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	run := func(ctx context.Context, op *operations.Operation) error {
		// Setup a reverter.
		revert := revert.New()
		defer revert.Fail()

		// Set node status to CREATED.
		err := evacuateClusterSetState(s, originName, db.ClusterMemberStateCreated)
		if err != nil {
			return err
		}

		// Ensure node is put into its previous state if anything fails.
		revert.Add(func() {
			_ = evacuateClusterSetState(s, originName, db.ClusterMemberStateEvacuated)
		})

		var source lxd.InstanceServer
		var sourceNode db.NodeInfo

		// Restore the networks.
		err = networkStartup(d.State, true)
		if err != nil {
			return err
		}

		if !skipInstances {
			// Restart the local instances.
			for _, inst := range localInstances {
				// Don't start instances which were stopped by the user.
				if inst.LocalConfig()["volatile.last_state.power"] != instance.PowerStateRunning {
					continue
				}

				// Don't attempt to start instances which are already running.
				if inst.IsRunning() {
					continue
				}

				// Start the instance.
				_ = op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)})

				err = inst.Start(false)
				if err != nil {
					return fmt.Errorf("Failed starting instance %q: %w", inst.Name(), err)
				}
			}

			// Migrate back the remote instances.
			for _, inst := range instances {
				l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

				// Check if live-migratable.
				_, live := inst.CanMigrate()

				_ = op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Migrating %q in project %q from %q", inst.Name(), inst.Project().Name, inst.Location())})

				err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					sourceNode, err = tx.GetNodeByName(ctx, inst.Location())
					if err != nil {
						return fmt.Errorf("Failed getting node %q: %w", inst.Location(), err)
					}

					return nil
				})
				if err != nil {
					return fmt.Errorf("Failed getting node: %w", err)
				}

				source, err = cluster.Connect(ctx, sourceNode.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
				if err != nil {
					return fmt.Errorf("Failed connecting to source: %w", err)
				}

				source = source.UseProject(inst.Project().Name)

				apiInst, _, err := source.GetInstance(inst.Name())
				if err != nil {
					return fmt.Errorf("Failed getting instance %q: %w", inst.Name(), err)
				}

				isRunning := apiInst.StatusCode == api.Running
				if isRunning && !live {
					_ = op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Stopping %q in project %q", inst.Name(), inst.Project().Name)})

					timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
					val, err := strconv.Atoi(timeout)
					if err != nil {
						val = evacuateHostShutdownDefaultTimeout
					}

					// Attempt a clean stop.
					stopOp, err := source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: false, Timeout: val}, "")
					if err != nil {
						return fmt.Errorf("Failed stopping instance %q: %w", inst.Name(), err)
					}

					// Wait for the stop operation to complete or timeout.
					err = stopOp.Wait()
					if err != nil {
						l.Warn("Failed shutting down instance, forcing stop", logger.Ctx{"err": err})

						// On failure, attempt a forceful stop.
						stopOp, err = source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: true}, "")
						if err != nil {
							// If this fails too, fail the whole operation.
							return fmt.Errorf("Failed stopping instance %q: %w", inst.Name(), err)
						}

						// Wait for the forceful stop to complete.
						err = stopOp.Wait()
						if err != nil && !strings.Contains(err.Error(), "The instance is already stopped") {
							return fmt.Errorf("Failed stopping instance %q: %w", inst.Name(), err)
						}
					}
				}

				req := api.InstancePost{
					Name:      inst.Name(),
					Migration: true,
					Live:      live,
				}

				source = source.UseTarget(originName)

				migrationOp, err := source.MigrateInstance(inst.Name(), req)
				if err != nil {
					return fmt.Errorf("Migration API failure: %w", err)
				}

				err = migrationOp.Wait()
				if err != nil {
					return fmt.Errorf("Failed waiting for migration to finish: %w", err)
				}

				// Reload the instance after migration.
				inst, err := instance.LoadByProjectAndName(s, inst.Project().Name, inst.Name())
				if err != nil {
					return fmt.Errorf("Failed loading instance: %w", err)
				}

				config := inst.LocalConfig()
				delete(config, "volatile.evacuate.origin")

				args := db.InstanceArgs{
					Architecture: inst.Architecture(),
					Config:       config,
					Description:  inst.Description(),
					Devices:      inst.LocalDevices(),
					Ephemeral:    inst.IsEphemeral(),
					Profiles:     inst.Profiles(),
					Project:      inst.Project().Name,
					ExpiryDate:   inst.ExpiryDate(),
				}

				err = inst.Update(args, false)
				if err != nil {
					return fmt.Errorf("Failed updating instance %q: %w", inst.Name(), err)
				}

				if !isRunning || live {
					continue
				}

				_ = op.ExtendMetadata(map[string]any{"evacuation_progress": fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)})

				err = inst.Start(false)
				if err != nil {
					return fmt.Errorf("Failed starting instance %q: %w", inst.Name(), err)
				}
			}

			logger.Info("Cluster member restored", logger.Ctx{"member": originName})
		} else {
			logger.Info("Cluster member restored (instances skipped)", logger.Ctx{"member": originName})
		}

		revert.Success()
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterMemberRestored.Event(originName, op.EventLifecycleRequestor(), nil))
		return nil
	}

	args := operations.OperationArgs{
		ProjectName: "",
		Type:        operationtype.ClusterMemberRestore,
		Class:       operations.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
