package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
)

var clusterGroupsCmd = APIEndpoint{
	Path:        "cluster/groups",
	MetricsType: entity.TypeClusterMember,

	Get:  APIEndpointAction{Handler: clusterGroupsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: clusterGroupsPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var clusterGroupCmd = APIEndpoint{
	Path:        "cluster/groups/{name}",
	MetricsType: entity.TypeClusterMember,

	Get:    APIEndpointAction{Handler: clusterGroupGet, AccessHandler: allowAuthenticated},
	Post:   APIEndpointAction{Handler: clusterGroupPost, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: clusterGroupPut, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Patch:  APIEndpointAction{Handler: clusterGroupPatch, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: clusterGroupDelete, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation POST /1.0/cluster/groups cluster cluster_groups_post
//
//	Create a cluster group.
//
//	Creates a new cluster group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster group to create
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	req := api.ClusterGroupsPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = validate.IsClusterGroupName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		obj := dbCluster.ClusterGroup{
			Name:        req.Name,
			Description: req.Description,
			Nodes:       req.Members,
		}

		_, err := dbCluster.CreateClusterGroup(ctx, tx.Tx(), obj)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return api.StatusErrorf(http.StatusConflict, "Cluster group %q already exists", req.Name)
			}

			return err
		}

		for _, node := range obj.Nodes {
			err = tx.AddNodeToClusterGroup(ctx, obj.Name, node)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ClusterGroupCreated.Event(req.Name, requestor, nil)
	s.Events.SendLifecycle("", lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/cluster/groups cluster-groups cluster_groups_get
//
//  Get the cluster groups
//
//  Returns a list of cluster groups (URLs).
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
//                "/1.0/cluster/groups/lxd01",
//                "/1.0/cluster/groups/lxd02"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/groups?recursion=1 cluster-groups cluster_groups_get_recursion1
//
//	Get the cluster groups
//
//	Returns a list of cluster groups (structs).
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
//	          description: List of cluster groups
//	          items:
//	            $ref: "#/definitions/ClusterGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	recursion, _ := util.IsRecursionRequest(r)

	var clusterGroupURIs []string
	var apiClusterGroups []*api.ClusterGroup
	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		if recursion > 0 {
			clusterGroups, err := dbCluster.GetClusterGroups(ctx, tx.Tx())
			if err != nil {
				return err
			}

			apiClusterGroups = make([]*api.ClusterGroup, 0, len(clusterGroups))
			for _, clusterGroup := range clusterGroups {
				nodeClusterGroups, err := dbCluster.GetNodeClusterGroups(ctx, tx.Tx(), dbCluster.NodeClusterGroupFilter{GroupID: &clusterGroup.ID})
				if err != nil {
					return err
				}

				clusterGroup.Nodes = make([]string, 0, len(nodeClusterGroups))
				for _, node := range nodeClusterGroups {
					clusterGroup.Nodes = append(clusterGroup.Nodes, node.Node)
				}

				apiClusterGroup, err := clusterGroup.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				usedBy, err := clusterGroupUsedBy(ctx, s, tx, clusterGroup.Name, false)
				if err != nil {
					return err
				}

				apiClusterGroup.UsedBy = usedBy
				apiClusterGroups = append(apiClusterGroups, apiClusterGroup)
			}
		} else {
			clusterGroupURIs, err = tx.GetClusterGroupURIs(ctx, dbCluster.ClusterGroupFilter{})
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, clusterGroupURIs)
	}

	for _, clusterGroup := range apiClusterGroups {
		clusterGroup.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, clusterGroup.UsedBy)
	}

	return response.SyncResponse(true, apiClusterGroups)
}

// swagger:operation GET /1.0/cluster/groups/{name} cluster-groups cluster_group_get
//
//	Get the cluster group
//
//	Gets a specific cluster group.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster group
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
//	          $ref: "#/definitions/ClusterGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	var group *dbCluster.ClusterGroup
	var apiGroup *api.ClusterGroup
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the cluster group.
		group, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		nodeClusterGroups, err := dbCluster.GetNodeClusterGroups(ctx, tx.Tx(), dbCluster.NodeClusterGroupFilter{GroupID: &group.ID})
		if err != nil {
			return err
		}

		group.Nodes = make([]string, 0, len(nodeClusterGroups))
		for _, node := range nodeClusterGroups {
			group.Nodes = append(group.Nodes, node.Node)
		}

		apiGroup, err = group.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		usedBy, err := clusterGroupUsedBy(ctx, s, tx, name, false)
		if err != nil {
			return err
		}

		apiGroup.UsedBy = usedBy
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	apiGroup.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, apiGroup.UsedBy)

	return response.SyncResponseETag(true, apiGroup, apiGroup.Writable())
}

// swagger:operation POST /1.0/cluster/groups/{name} cluster-groups cluster_group_post
//
//	Rename the cluster group
//
//	Renames an existing cluster group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: name
//	    description: Cluster group rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGroupPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if name == "default" {
		return response.Forbidden(errors.New(`The "default" group cannot be renamed`))
	}

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	req := api.ClusterGroupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = validate.IsClusterGroupName(name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), req.Name)
		if err == nil {
			return fmt.Errorf("Name %q already in use", req.Name)
		}

		usedBy, err := clusterGroupUsedBy(r.Context(), s, tx, name, true)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Cluster group is currently in use")
		}

		// Rename the cluster group.
		err = dbCluster.RenameClusterGroup(ctx, tx.Tx(), name, req.Name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ClusterGroupRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": name})
	s.Events.SendLifecycle("", lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation PUT /1.0/cluster/groups/{name} cluster-groups cluster_group_put
//
//	Update the cluster group
//
//	Updates the entire cluster group configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster group
//	    description: cluster group configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupPut"
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
func clusterGroupPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	req := api.ClusterGroupPut{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		group, err := dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		members, err := tx.GetClusterGroupNodes(ctx, name)
		if err != nil {
			return err
		}

		// Every member must belong to at least one group.
		for _, oldMember := range members {
			// On removing member, ensure it is in at least one other group.
			if !slices.Contains(req.Members, oldMember) {
				// Get all cluster groups this member belongs to.
				groups, err := tx.GetClusterGroupsWithNode(ctx, oldMember)
				if err != nil {
					return err
				}

				if len(groups) == 1 {
					return fmt.Errorf("Cannot remove %q from group as member needs to belong to at least one group", oldMember)
				}
			}
		}

		obj := dbCluster.ClusterGroup{
			Name:        group.Name,
			Description: req.Description,
		}

		err = dbCluster.UpdateClusterGroup(ctx, tx.Tx(), name, obj)
		if err != nil {
			return err
		}

		// skipMembers is a list of members which already belong to the group.
		skipMembers := []string{}

		for _, oldMember := range members {
			if !slices.Contains(req.Members, oldMember) {
				// Remove member from this group. It belongs to at least one other group per the check above.
				err = tx.RemoveNodeFromClusterGroup(ctx, name, oldMember)
				if err != nil {
					return err
				}
			} else {
				skipMembers = append(skipMembers, oldMember)
			}
		}

		for _, member := range req.Members {
			// Skip these members as they already belong to this group.
			if slices.Contains(skipMembers, member) {
				continue
			}

			// Add new members to the group.
			err = tx.AddNodeToClusterGroup(ctx, name, member)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle("", lifecycle.ClusterGroupUpdated.Event(name, requestor, logger.Ctx{"description": req.Description, "members": req.Members}))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/cluster/groups/{name} cluster-groups cluster_group_patch
//
//	Update the cluster group
//
//	Updates the cluster group configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster group
//	    description: cluster group configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterGroupPut"
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
func clusterGroupPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	var clusterGroup *api.ClusterGroup
	var dbClusterGroup *dbCluster.ClusterGroup

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbClusterGroup, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		nodeClusterGroups, err := dbCluster.GetNodeClusterGroups(ctx, tx.Tx(), dbCluster.NodeClusterGroupFilter{GroupID: &dbClusterGroup.ID})
		if err != nil {
			return err
		}

		dbClusterGroup.Nodes = make([]string, 0, len(nodeClusterGroups))
		for _, node := range nodeClusterGroups {
			dbClusterGroup.Nodes = append(dbClusterGroup.Nodes, node.Node)
		}

		clusterGroup, err = dbClusterGroup.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	req := clusterGroup.Writable()

	// Validate the ETag.
	etag := []any{clusterGroup.Description, clusterGroup.Members}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Members == nil {
		req.Members = clusterGroup.Members
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		obj := dbCluster.ClusterGroup{
			Name:        dbClusterGroup.Name,
			Description: req.Description,
		}

		err = dbCluster.UpdateClusterGroup(ctx, tx.Tx(), name, obj)
		if err != nil {
			return err
		}

		groupID, err := dbCluster.GetClusterGroupID(ctx, tx.Tx(), obj.Name)
		if err != nil {
			return err
		}

		err = dbCluster.DeleteNodeClusterGroup(ctx, tx.Tx(), int(groupID))
		if err != nil {
			return err
		}

		for _, node := range obj.Nodes {
			err = tx.AddNodeToClusterGroup(ctx, obj.Name, node)
			if err != nil {
				return err
			}
		}

		members, err := tx.GetClusterGroupNodes(ctx, name)
		if err != nil {
			return err
		}

		// skipMembers is a list of members which already belong to the group.
		skipMembers := []string{}

		for _, oldMember := range members {
			if !slices.Contains(req.Members, oldMember) {
				// Get all cluster groups this member belongs to.
				groups, err := tx.GetClusterGroupsWithNode(ctx, oldMember)
				if err != nil {
					return err
				}

				// Cluster member cannot be removed from the group as it doesn't belong to any other.
				if len(groups) == 1 {
					return fmt.Errorf("Cannot remove %q from group as member needs to belong to at least one group", oldMember)
				}

				// Remove member from this group as it belongs to at least one other group.
				err = tx.RemoveNodeFromClusterGroup(ctx, name, oldMember)
				if err != nil {
					return err
				}
			} else {
				skipMembers = append(skipMembers, oldMember)
			}
		}

		for _, member := range req.Members {
			// Skip these members as they already belong to this group.
			if slices.Contains(skipMembers, member) {
				continue
			}

			// Add new members to the group.
			err = tx.AddNodeToClusterGroup(ctx, name, member)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle("", lifecycle.ClusterGroupUpdated.Event(name, requestor, logger.Ctx{"description": req.Description, "members": req.Members}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/groups/{name} cluster-groups cluster_group_delete
//
//	Delete the cluster group.
//
//	Removes the cluster group.
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
func clusterGroupDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	if name == "default" {
		return response.Forbidden(errors.New("The 'default' cluster group cannot be deleted"))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetClusterGroupNodes(ctx, name)
		if err != nil {
			return err
		}

		if len(members) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Only empty cluster groups can be removed")
		}

		usedBy, err := clusterGroupUsedBy(r.Context(), s, tx, name, true)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Cluster group is currently in use")
		}

		return dbCluster.DeleteClusterGroup(ctx, tx.Tx(), name)
	})

	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(name, lifecycle.ClusterGroupDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// clusterGroupUsedBy returns the list of resource URLs that reference the cluster group.
// The returned slice contains project URLs (for projects whose "restricted.cluster.groups" configuration includes the group) and instance URLs (for instances whose config contains the group in the "volatile.cluster.group" key).
// If firstOnly is true then search stops at first result.
func clusterGroupUsedBy(ctx context.Context, s *state.State, tx *db.ClusterTx, name string, firstOnly bool) ([]string, error) {
	var usedBy []string
	var err error

	usedBy, err = dbCluster.GetProjectsUsingRestrictedClusterGroups(ctx, tx.Tx(), name)
	if err != nil {
		return nil, err
	}

	if len(usedBy) > 0 && firstOnly {
		return usedBy[:1], nil
	}

	err = tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
		// Check if instance references cluster group in "volatile.cluster.group" config key.
		if inst.Config["volatile.cluster.group"] == name {
			u := entity.InstanceURL(inst.Project, inst.Name)

			// Omit the project query parameter if it is the default project.
			if u.Query().Get("project") == api.ProjectDefaultName {
				q := u.Query()
				q.Del("project")
				u.RawQuery = q.Encode()
			}

			usedBy = append(usedBy, u.String())

			if firstOnly {
				return db.ErrListStop
			}
		}

		return nil
	})
	if err != nil && err != db.ErrListStop {
		return nil, fmt.Errorf("Failed getting instances: %w", err)
	}

	return usedBy, nil
}
