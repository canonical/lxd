package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
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

var placementGroupsCmd = APIEndpoint{
	Path:        "placement-groups",
	MetricsType: entity.TypePlacementGroup,

	Get:  APIEndpointAction{Handler: placementGroupsGet, AccessHandler: allowProjectResourceList(false)},
	Post: APIEndpointAction{Handler: placementGroupsPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreatePlacementGroups)},
}

var placementGroupCmd = APIEndpoint{
	Path:        "placement-groups/{name}",
	MetricsType: entity.TypePlacementGroup,

	Delete: APIEndpointAction{Handler: placementGroupDelete, AccessHandler: allowPermission(entity.TypePlacementGroup, auth.EntitlementCanDelete, "name")},
	Get:    APIEndpointAction{Handler: placementGroupGet, AccessHandler: allowPermission(entity.TypePlacementGroup, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: placementGroupPut, AccessHandler: allowPermission(entity.TypePlacementGroup, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: placementGroupPut, AccessHandler: allowPermission(entity.TypePlacementGroup, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: placementGroupPost, AccessHandler: allowPermission(entity.TypePlacementGroup, auth.EntitlementCanEdit, "name")},
}

var placementGroupRebalanceCmd = APIEndpoint{
	Path:        "placement-groups/{name}/rebalance",
	MetricsType: entity.TypePlacementGroup,

	Post: APIEndpointAction{Handler: placementGroupRebalancePost, AccessHandler: allowPermission(entity.TypePlacementGroup, auth.EntitlementCanEdit, "name")},
}

// API endpoints.

// swagger:operation GET /1.0/placement-groups placement-groups placement_groups_get
//
//  Get the placement groups
//
//  Returns a list of placement groups (URLs).
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
//      description: Retrieve placement groups from all projects
//      type: boolean
//      example: true
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
//                "/1.0/placement-groups/group1",
//                "/1.0/placement-groups/group2"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/placement-groups?recursion=1 placement-groups placement_groups_get_recursion1
//
//	Get the placement groups
//
//	Returns a list of placement groups (structs).
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
//	    description: Retrieve placement groups from all projects
//	    type: boolean
//	    example: true
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
//	          description: List of placement groups
//	          items:
//	            $ref: "#/definitions/PlacementGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementGroupsGet(d *Daemon, r *http.Request) response.Response {
	projectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	recursion, _ := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypePlacementGroup, true)
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	canViewPlacementGroup, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypePlacementGroup)
	if err != nil {
		return response.InternalError(err)
	}

	var apiGroups []*api.PlacementGroup
	var placementGroupURLs []string
	entitlementReportingMap := make(map[*api.URL]auth.EntitlementReporter)
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var projectNameFilter *string
		if !allProjects {
			projectNameFilter = &projectName
		}

		var placementGroups []cluster.PlacementGroup
		placementGroups, placementGroupURLs, err = cluster.GetPlacementGroupsAndURLs(ctx, tx.Tx(), projectNameFilter, func(group cluster.PlacementGroup) bool {
			return canViewPlacementGroup(entity.PlacementGroupURL(group.ProjectName, group.Row.Name))
		})

		if recursion == 0 {
			return nil
		}

		// Transaction local variable to prevent appending duplicates in case of transaction retry on sqlite.ErrBusy.
		apiGroupsTx := make([]*api.PlacementGroup, 0, len(placementGroups))
		for _, placementGroup := range placementGroups {
			u := entity.PlacementGroupURL(placementGroup.ProjectName, placementGroup.Row.Name)
			apiGroup, err := placementGroup.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			filter := cluster.PlacementGroupFilter{
				Project: &placementGroup.ProjectName,
				Name:    &placementGroup.Row.Name,
			}

			usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), filter, false)
			if err != nil {
				return err
			}

			apiGroup.UsedBy = usedBy
			apiGroupsTx = append(apiGroupsTx, apiGroup)
			entitlementReportingMap[u] = apiGroup
		}

		apiGroups = apiGroupsTx
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, placementGroupURLs)
	}

	for _, pg := range apiGroups {
		pg.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, pg.UsedBy)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypePlacementGroup, withEntitlements, entitlementReportingMap)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiGroups)
}

// swagger:operation POST /1.0/placement-groups placement-groups placement_groups_post
//
//	Add a placement group
//
//	Creates a new placement group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: placementGroup
//	    description: The new placement group
//	    required: true
//	    schema:
//	      $ref: "#/definitions/PlacementGroupsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementGroupsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	req := api.PlacementGroupsPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validate.IsDeviceName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName := request.ProjectParam(r)
	newGroup := cluster.PlacementGroupsRow{
		Name:        req.Name,
		Description: req.Description,
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := placementGroupValidateConfig(req.Config)
		if err != nil {
			return err
		}

		// The project ID should already be in scope or context, because we have already checked if the caller has access to it.
		// Since it currently isn't available, get it to perform the creation.
		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed getting project ID: %w", err)
		}

		newGroup.ProjectID = projectID
		id, err := query.Create(ctx, tx.Tx(), newGroup)
		if err != nil {
			return err
		}

		err = cluster.CreatePlacementGroupConfig(ctx, tx.Tx(), id, req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	lc := lifecycle.PlacementGroupCreated.Event(projectName, req.Name, request.CreateRequestor(r.Context()), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/placement-groups/{name} placement-groups placement_group_delete
//
//	Delete the placement group
//
//	Removes the placement group.
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
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementGroupDelete(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	placementGroupName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	err = doPlacementGroupDelete(r.Context(), s, placementGroupName, projectName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func doPlacementGroupDelete(ctx context.Context, s *state.State, name string, projectName string) error {
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbGroup, err := cluster.GetPlacementGroup(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return err
		}

		filter := cluster.PlacementGroupFilter{
			Project: &dbGroup.ProjectName,
			Name:    &dbGroup.Row.Name,
		}

		usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), filter, true)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Placement group %q is currently in use", name)
		}

		return query.DeleteByPrimaryKey(ctx, tx.Tx(), dbGroup.Row)
	})
	if err != nil {
		return err
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementGroupDeleted.Event(projectName, name, request.CreateRequestor(ctx), nil))

	return nil
}

// swagger:operation GET /1.0/placement-groups/{name} placement-groups placement_group_get
//
//	Get the placement group
//
//	Gets a specific placement group.
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
//	responses:
//	  "200":
//	    description: zone
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
//	          $ref: "#/definitions/PlacementGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementGroupGet(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	placementGroupName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypePlacementGroup, false)
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	var placementGroup *api.PlacementGroup
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbGroup, err := cluster.GetPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		placementGroup, err = dbGroup.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		filter := cluster.PlacementGroupFilter{
			Project: &dbGroup.ProjectName,
			Name:    &dbGroup.Row.Name,
		}

		usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), filter, false)
		if err != nil {
			return err
		}

		placementGroup.UsedBy = usedBy

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	etag := *placementGroup
	placementGroup.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, placementGroup.UsedBy)
	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypePlacementGroup, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.PlacementGroupURL(projectName, placementGroupName): placementGroup})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, placementGroup, etag)
}

// swagger:operation PATCH /1.0/placement-groups/{name} placement-groups placement_group_patch
//
//  Partially update the placement group
//
//  Updates a subset of the placement group configuration.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: body
//      name: placement group
//      description: placement group
//      required: true
//      schema:
//        $ref: "#/definitions/PlacementGroupPut"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "412":
//      $ref: "#/responses/PreconditionFailed"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation PUT /1.0/placement-groups/{name} placement-groups placement_group_put
//
//	Update the placement group
//
//	Updates the entire placement group.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: placement group
//	    description: placement group
//	    required: true
//	    schema:
//	      $ref: "#/definitions/PlacementGroupPut"
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
func placementGroupPut(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	placementGroupName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.PlacementGroupPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	s := d.State()

	var placementGroup *cluster.PlacementGroup
	var config map[string]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		placementGroup, err = cluster.GetPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		config, err = cluster.GetPlacementGroupConfig(ctx, tx.Tx(), placementGroup.Row.ID)
		if err != nil {
			return fmt.Errorf("Failed getting placement group config: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = util.EtagCheck(r, placementGroup)
	if err != nil {
		return response.SmartError(err)
	}

	updatedPlacementGroup := *placementGroup
	updatedConfig := config
	var descriptionChanged bool
	switch r.Method {
	case http.MethodPut:
		if req.Description != updatedPlacementGroup.Row.Description {
			updatedPlacementGroup.Row.Description = req.Description
			descriptionChanged = true
		}

		updatedConfig = req.Config
	case http.MethodPatch:
		if req.Description != "" {
			descriptionChanged = true
			updatedPlacementGroup.Row.Description = req.Description
		}

		// Merge config
		for k, v := range req.Config {
			if v == "" {
				// PATCH with empty value unsets the key.
				delete(updatedConfig, k)
				continue
			}

			updatedConfig[k] = v
		}
	}

	err = placementGroupValidateConfig(updatedConfig)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if descriptionChanged {
			err = query.UpdateByPrimaryKey(ctx, tx.Tx(), updatedPlacementGroup.Row)
			if err != nil {
				return err
			}
		}

		err = cluster.UpdatePlacementGroupConfig(ctx, tx.Tx(), updatedPlacementGroup.Row.ID, updatedConfig)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementGroupUpdated.Event(projectName, placementGroupName, request.CreateRequestor(r.Context()), nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/placement-groups/{name} placement-groups placement_group_post
//
//	Rename a placement group
//
//	Renames the placement group.
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
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementGroupPost(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	placementGroupName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.PlacementGroupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validate.IsDeviceName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	s := d.State()

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbGroup, err := cluster.GetPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		filter := cluster.PlacementGroupFilter{
			Project: &dbGroup.ProjectName,
			Name:    &dbGroup.Row.Name,
		}

		usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), filter, true)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Placement group %q is currently in use", placementGroupName)
		}

		dbGroup.Row.Name = req.Name
		return query.UpdateByPrimaryKey(ctx, tx.Tx(), dbGroup.Row)
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementGroupRenamed.Event(projectName, placementGroupName, request.CreateRequestor(r.Context()), nil))

	return response.SyncResponseLocation(true, nil, entity.PlacementGroupURL(projectName, placementGroupName).String())
}

// placementGroupValidateConfig validates the configuration keys/values for placement groups.
func placementGroupValidateConfig(config map[string]string) error {
	placementGroupConfigKeys := map[string]func(value string) error{
		// lxdmeta:generate(entities=placement-group; group=placement-group; key=policy)
		// Determines whether instances are spread across cluster members or
		// compacted onto the same cluster member(s).
		//
		// Possible values are `spread` and `compact`.
		// See {ref}`clustering-instance-placement` for more information.
		// ---
		//  type: string
		//  required: "yes"
		//  shortdesc: Instance placement policy
		"policy": validate.IsOneOf(api.PlacementPolicySpread, api.PlacementPolicyCompact),

		// lxdmeta:generate(entities=placement-group; group=placement-group; key=rigor)
		// Determines whether the policy is strictly enforced or allows fallback.
		//
		// Possible values are `strict` and `permissive`.
		// See {ref}`clustering-instance-placement` for more information.
		// ---
		//  type: string
		//  required: "yes"
		//  shortdesc: Enforcement level of the placement policy
		"rigor": validate.IsOneOf(api.PlacementRigorStrict, api.PlacementRigorPermissive),
	}

	for k, v := range config {
		// lxdmeta:generate(entities=placement-group; group=placement-group; key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := placementGroupConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid placement group key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid placement group key %q value", k)
		}
	}

	// Policy and rigor are both required.
	for _, k := range []string{"policy", "rigor"} {
		_, found := config[k]
		if !found {
			return api.StatusErrorf(http.StatusBadRequest, "Missing required config key: %q", k)
		}
	}

	return nil
}

// swagger:operation POST /1.0/placement-groups/{name}/rebalance placement-groups placement_group_rebalance_post
//
//	Rebalance a placement group
//
//	Triggers rebalancing of instances in the given placement group according to its placement policy.
//	Instances that do not comply with the current policy and rigor are migrated to more appropriate cluster members.
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
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementGroupRebalancePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	projectName := request.ProjectParam(r)
	placementGroupName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Verify the placement group exists.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.GetPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	migrateFunc := func(ctx context.Context, s *state.State, inst instance.Instance, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, op *operations.Operation) error {
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

		// Start the instance on the target.
		dest, err := lxdCluster.Connect(ctx, targetMemberInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
		if err != nil {
			return fmt.Errorf("Failed connecting to destination %q for instance %q in project %q: %w", targetMemberInfo.Address, inst.Name(), inst.Project().Name, err)
		}

		dest = dest.UseProject(inst.Project().Name)

		if op != nil {
			_ = op.ExtendMetadata(map[string]any{"rebalance_progress": fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)})
		}

		startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
		if err != nil {
			return err
		}

		return startOp.Wait()
	}

	run := func(ctx context.Context, op *operations.Operation) error {
		return doPlacementGroupRebalance(ctx, d, op, placementGroupName, projectName, migrateFunc)
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		Type:        operationtype.PlacementGroupRebalance,
		Class:       operations.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationResponse(op)
}

// rebalanceMigration represents a planned instance migration for rebalancing.
type rebalanceMigration struct {
	instanceID   int
	targetMember *db.NodeInfo
}

// doPlacementGroupRebalance performs the rebalancing of instances in a placement group.
// It determines which instances are out of compliance with the placement policy and migrates
// them to more appropriate cluster members.
func doPlacementGroupRebalance(ctx context.Context, d *Daemon, op *operations.Operation, placementGroupName string, projectName string, migrateInstance evacuateMigrateFunc) error {
	s := d.State()

	// Load placement group.
	var apiPlacementGroup *api.PlacementGroup
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbGroup, err := cluster.GetPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		apiPlacementGroup, err = dbGroup.ToAPI(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return err
	}

	policy := apiPlacementGroup.Config["policy"]
	rigor := apiPlacementGroup.Config["rigor"]

	// Get candidate members and the current instance placement.
	var memberToInstIDs map[int64][]int64
	var candidateMembers []db.NodeInfo

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		allMembers, err := tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, nil, "", nil, s.GlobalConfig.OfflineThreshold())
		if err != nil {
			return err
		}

		memberToInstIDs, err = cluster.GetInstancesInPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName, nil)
		return err
	})
	if err != nil {
		return err
	}

	if len(memberToInstIDs) == 0 {
		// No instances in placement group, nothing to rebalance.
		return nil
	}

	// Build member ID → NodeInfo map for quick lookup.
	memberIDToInfo := make(map[int64]*db.NodeInfo, len(candidateMembers))
	for i := range candidateMembers {
		memberIDToInfo[candidateMembers[i].ID] = &candidateMembers[i]
	}

	// Compute the migration plan based on the placement policy.
	migrations, err := computeRebalanceMigrations(policy, rigor, memberToInstIDs, candidateMembers, memberIDToInfo)
	if err != nil {
		return err
	}

	if len(migrations) == 0 {
		logger.Debug("Placement group is already balanced, no migrations needed", logger.Ctx{"placementGroup": placementGroupName, "project": projectName})
		return nil
	}

	// Execute migrations.
	for _, migration := range migrations {
		inst, err := instance.LoadByID(s, migration.instanceID)
		if err != nil {
			return fmt.Errorf("Failed loading instance with ID %d: %w", migration.instanceID, err)
		}

		l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

		migrate, live := inst.CanMigrate()
		if !migrate {
			l.Info("Skipping non-migratable instance during placement group rebalance")
			continue
		}

		if op != nil {
			_ = op.ExtendMetadata(map[string]any{"rebalance_progress": fmt.Sprintf("Migrating %q in project %q to %q", inst.Name(), inst.Project().Name, migration.targetMember.Name)})
		}

		start := inst.IsRunning() || instanceShouldAutoStart(inst)
		err = migrateInstance(ctx, s, inst, migration.targetMember, live, start, op)
		if err != nil {
			return err
		}
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementGroupRebalanced.Event(projectName, placementGroupName, request.CreateRequestor(ctx), nil))

	return nil
}

// computeRebalanceMigrations determines which instances need to be migrated to rebalance the placement group.
// It returns a list of (instanceID, targetMember) pairs representing the planned migrations.
func computeRebalanceMigrations(policy string, rigor string, memberToInstIDs map[int64][]int64, candidateMembers []db.NodeInfo, memberIDToInfo map[int64]*db.NodeInfo) ([]rebalanceMigration, error) {
	var migrations []rebalanceMigration

	switch policy {
	case api.PlacementPolicyCompact:
		// Find the member with the most instances (this is the compact target).
		var targetMemberID int64
		maxCount := -1
		for memberID, instIDs := range memberToInstIDs {
			if len(instIDs) > maxCount {
				maxCount = len(instIDs)
				targetMemberID = memberID
			}
		}

		// The target member must be an online candidate.
		targetMember, ok := memberIDToInfo[targetMemberID]
		if !ok {
			if rigor == api.PlacementRigorStrict {
				return nil, fmt.Errorf("Target cluster member for compact placement group is unavailable")
			}

			// Permissive: pick the first available candidate as the target.
			if len(candidateMembers) == 0 {
				return nil, errors.New("No candidate cluster members available for rebalance")
			}

			targetMember = &candidateMembers[0]
			targetMemberID = targetMember.ID
		}

		// All instances on online non-target members should be migrated to the target.
		for memberID, instIDs := range memberToInstIDs {
			if memberID == targetMemberID {
				continue
			}

			// Only migrate instances from online (candidate) members.
			if _, isCandidate := memberIDToInfo[memberID]; !isCandidate {
				continue
			}

			for _, instID := range instIDs {
				migrations = append(migrations, rebalanceMigration{int(instID), targetMember})
			}
		}

	case api.PlacementPolicySpread:
		// Build a mutable count map for online candidate members.
		countPerMember := make(map[int64]int, len(candidateMembers))
		for _, m := range candidateMembers {
			countPerMember[m.ID] = len(memberToInstIDs[m.ID])
		}

		// Compute total instances across all candidate members.
		totalInstances := 0
		for _, m := range candidateMembers {
			totalInstances += countPerMember[m.ID]
		}

		nCandidates := len(candidateMembers)
		if nCandidates == 0 {
			return nil, errors.New("No candidate cluster members available for rebalance")
		}

		// Determine the maximum number of instances allowed per member.
		// For strict: max 1 per member.
		// For permissive: ceil(total / candidates).
		maxPerMember := 1
		if rigor == api.PlacementRigorPermissive {
			maxPerMember = (totalInstances + nCandidates - 1) / nCandidates
		}

		// For each online candidate member that is overloaded, select instances to migrate.
		for _, m := range candidateMembers {
			instIDs := memberToInstIDs[m.ID]
			excess := len(instIDs) - maxPerMember
			if excess <= 0 {
				continue
			}

			// Migrate the excess instances to the least-loaded candidate members.
			for _, instID := range instIDs[:excess] {
				// Find the candidate member with the fewest instances (excluding the current member).
				var targetMember *db.NodeInfo
				minCount := math.MaxInt
				for i := range candidateMembers {
					if candidateMembers[i].ID == m.ID {
						continue
					}

					if countPerMember[candidateMembers[i].ID] < minCount {
						minCount = countPerMember[candidateMembers[i].ID]
						targetMember = &candidateMembers[i]
					}
				}

				if targetMember == nil {
					continue
				}

				// For strict: only migrate if the target has fewer instances than allowed.
				if rigor == api.PlacementRigorStrict && minCount >= maxPerMember {
					logger.Debug("Insufficient empty cluster members for strict spread rebalance, skipping instance",
						logger.Ctx{"instanceID": instID, "currentMember": m.Name})
					continue
				}

				migrations = append(migrations, rebalanceMigration{int(instID), targetMember})

				// Optimistically update counts to guide subsequent target selection.
				countPerMember[m.ID]--
				countPerMember[targetMember.ID]++
			}
		}

	default:
		return nil, fmt.Errorf("Invalid placement group policy: %q", policy)
	}

	return migrations, nil
}
