package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
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
		var projectFilter *string
		if !allProjects {
			projectFilter = &projectName
		}

		if recursion == 0 {
			placementGroups, err := cluster.GetPlacementGroups(ctx, tx.Tx(), cluster.PlacementGroupFilter{Project: projectFilter})
			if err != nil {
				return err
			}

			for _, placementGroup := range placementGroups {
				u := entity.PlacementGroupURL(projectName, placementGroup.Name)
				if !canViewPlacementGroup(u) {
					continue
				}

				placementGroupURLs = append(placementGroupURLs, u.String())
			}

			return nil
		}

		var filters []cluster.PlacementGroupFilter
		if !allProjects {
			filters = append(filters, cluster.PlacementGroupFilter{Project: &projectName})
		}

		placementGroups, err := cluster.GetPlacementGroups(ctx, tx.Tx(), filters...)
		if err != nil {
			return err
		}

		apiGroups = make([]*api.PlacementGroup, 0, len(placementGroups))
		for _, placementGroup := range placementGroups {
			u := entity.PlacementGroupURL(placementGroup.Project, placementGroup.Name)
			if !canViewPlacementGroup(u) {
				continue
			}

			apiGroup, err := placementGroup.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), cluster.PlacementGroupFilter{Project: &placementGroup.Project, Name: &placementGroup.Name}, false)
			if err != nil {
				return err
			}

			apiGroup.UsedBy = usedBy
			apiGroups = append(apiGroups, apiGroup)
			entitlementReportingMap[u] = apiGroup
		}

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
	newGroup := cluster.PlacementGroup{
		Name:        req.Name,
		Description: req.Description,
		Project:     projectName,
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := placementGroupValidateConfig(req.Config)
		if err != nil {
			return err
		}

		id, err := cluster.CreatePlacementGroup(ctx, tx.Tx(), newGroup)
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
		_, err := cluster.GetPlacementGroupID(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return err
		}

		usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), cluster.PlacementGroupFilter{Project: &projectName, Name: &name}, true)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Placement group %q is currently in use", name)
		}

		return cluster.DeletePlacementGroup(ctx, tx.Tx(), name, projectName)
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

		usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), cluster.PlacementGroupFilter{Project: &placementGroup.Project, Name: &placementGroup.Name}, false)
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

	var existing *api.PlacementGroup
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbGroup, err := cluster.GetPlacementGroup(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		existing, err = dbGroup.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = util.EtagCheck(r, existing)
	if err != nil {
		return response.SmartError(err)
	}

	duplicate := *existing
	switch r.Method {
	case http.MethodPut:
		duplicate.Description = req.Description
		duplicate.Config = req.Config
	case http.MethodPatch:
		if req.Description != "" {
			duplicate.Description = req.Description
		}

		// Merge config
		if req.Config == nil {
			req.Config = existing.Config
		} else {
			for k, v := range existing.Config {
				_, ok := req.Config[k]
				if !ok {
					req.Config[k] = v
				}
			}
		}

		duplicate.Config = req.Config
	}

	err = placementGroupValidateConfig(duplicate.Config)
	if err != nil {
		return response.SmartError(err)
	}

	dbPlacementGroup := cluster.PlacementGroup{
		Name:        duplicate.Name,
		Description: duplicate.Description,
		Project:     duplicate.Project,
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err = cluster.UpdatePlacementGroup(ctx, tx.Tx(), placementGroupName, projectName, dbPlacementGroup)
		if err != nil {
			return err
		}

		id, err := cluster.GetPlacementGroupID(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		err = cluster.UpdatePlacementGroupConfig(ctx, tx.Tx(), id, duplicate.Config)
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
		_, err := cluster.GetPlacementGroupID(ctx, tx.Tx(), placementGroupName, projectName)
		if err != nil {
			return err
		}

		usedBy, err := cluster.GetPlacementGroupUsedBy(ctx, tx.Tx(), cluster.PlacementGroupFilter{Project: &projectName, Name: &placementGroupName}, true)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Placement group %q is currently in use", placementGroupName)
		}

		return cluster.RenamePlacementGroup(ctx, tx.Tx(), placementGroupName, projectName, req.Name)
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
