package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/placement"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/validate"
)

var placementRulesetsCmd = APIEndpoint{
	Path:        "placement-rulesets",
	MetricsType: entity.TypePlacementRuleset,

	Get:  APIEndpointAction{Handler: placementRulesetsGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: placementRulesetsPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreatePlacementRulesets)},
}

var placementRulesetCmd = APIEndpoint{
	Path:        "placement-rulesets/{rulesetName}",
	MetricsType: entity.TypePlacementRuleset,

	Delete: APIEndpointAction{Handler: placementRulesetDelete, AccessHandler: allowPermission(entity.TypePlacementRuleset, auth.EntitlementCanDelete, "rulesetName")},
	Get:    APIEndpointAction{Handler: placementRulesetGet, AccessHandler: allowPermission(entity.TypePlacementRuleset, auth.EntitlementCanView, "rulesetName")},
	Put:    APIEndpointAction{Handler: placementRulesetPut, AccessHandler: allowPermission(entity.TypePlacementRuleset, auth.EntitlementCanEdit, "rulesetName")},
	Patch:  APIEndpointAction{Handler: placementRulesetPut, AccessHandler: allowPermission(entity.TypePlacementRuleset, auth.EntitlementCanEdit, "rulesetName")},
	Post:   APIEndpointAction{Handler: placementRulesetPost, AccessHandler: allowPermission(entity.TypePlacementRuleset, auth.EntitlementCanEdit, "rulesetName")},
}

// API endpoints.

// swagger:operation GET /1.0/placement-rulesets placement-rulesets placement_rulesets_get
//
//  Get the placement rulesets
//
//  Returns a list of placement rulesets (URLs).
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
//      description: Retrieve placement rulesets from all projects
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
//                "/1.0/placement-rulesets/rule1",
//                "/1.0/placement-rulesets/rule2"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/placement-rulesets?recursion=1 placement-rulesets placement_rulesets_get_recursion1
//
//	Get the placement rulesets
//
//	Returns a list of placement rulesets (structs).
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
//	    description: Retrieve placement rulesets from all projects
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
//	          description: List of placement rulesets
//	          items:
//	            $ref: "#/definitions/PlacementRuleset"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementRulesetsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	allProjects := shared.IsTrue(request.QueryParam(r, "all-projects"))
	projectName := request.QueryParam(r, "project")
	if allProjects && projectName != "" {
		return response.BadRequest(errors.New("Cannot specify a project when requesting all projects"))
	}

	if !allProjects && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	recursion := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypePlacementRuleset, true)
	if err != nil {
		return response.SmartError(err)
	}

	canViewRuleset, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypePlacementRuleset)
	if err != nil {
		return response.InternalError(err)
	}

	var rulesets []cluster.PlacementRuleset
	var usedByMap map[string]map[string][]string
	var rulesetNames map[string][]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if !recursion {
			var project *string
			if !allProjects {
				project = &projectName
			}

			rulesetNames, err = cluster.GetPlacementRulesetNames(ctx, tx.Tx(), project)
			return err
		}

		var filter *cluster.PlacementRulesetFilter
		if !allProjects {
			filter = &cluster.PlacementRulesetFilter{Project: &projectName}
		}

		rulesets, err = cluster.GetPlacementRulesets(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		usedByMap, err = cluster.GetAllPlacementRulesetUsedByURLs(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		var urls []string
		for projectName, rulesets := range rulesetNames {
			for _, ruleset := range rulesets {
				u := api.NewURL().Project(projectName).Path("1.0", "placement-rulesets", ruleset)
				if !canViewRuleset(u) {
					continue
				}

				urls = append(urls, u.String())
			}
		}

		return response.SyncResponse(true, urls)
	}

	apiRulesets := make([]*api.PlacementRuleset, 0, len(rulesets))
	entitlementReportingMap := make(map[*api.URL]auth.EntitlementReporter)
	for _, ruleset := range rulesets {
		u := entity.PlacementRulesetURL(ruleset.Project, ruleset.Name)
		if !canViewRuleset(u) {
			continue
		}

		apiRuleset := ruleset.ToAPI()
		apiRuleset.UsedBy = project.FilterUsedBy(s.Authorizer, r, usedByMap[ruleset.Project][ruleset.Name])
		apiRulesets = append(apiRulesets, &apiRuleset)
		entitlementReportingMap[u] = &apiRuleset
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypePlacementRuleset, withEntitlements, entitlementReportingMap)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiRulesets)
}

// swagger:operation POST /1.0/placement-rulesets placement-rulesets placement_rulesets_post
//
//	Add a placement ruleset
//
//	Creates a new placement ruleset.
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
//	    name: ruleset
//	    description: The new placement ruleset
//	    required: true
//	    schema:
//	      $ref: "#/definitions/PlacementRulesetsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementRulesetsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	req := api.PlacementRulesetsPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName := request.ProjectParam(r)
	newRuleset := api.PlacementRuleset{
		Project:        projectName,
		Name:           req.Name,
		Description:    req.Description,
		PlacementRules: req.PlacementRules,
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRuleset, _, _, err := placement.ValidateRuleset(ctx, tx, projectName, newRuleset)
		if err != nil {
			return err
		}

		_, err = cluster.CreatePlacementRuleset(ctx, tx.Tx(), *dbRuleset)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	lc := lifecycle.PlacementRulesetCreated.Event(projectName, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/placement-rulesets/{name} placement-rulesets placement_ruleset_delete
//
//	Delete the placement ruleset
//
//	Removes the placement ruleset.
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
func placementRulesetDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	projectName := request.ProjectParam(r)
	rulesetName, err := url.PathUnescape(mux.Vars(r)["rulesetName"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.GetPlacementRulesetID(ctx, tx.Tx(), projectName, rulesetName)
		if err != nil {
			return err
		}

		usedBy, err := cluster.GetPlacementRulesetUsedBy(ctx, tx.Tx(), projectName, rulesetName)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Placement ruleset %q is currently in use", rulesetName)
		}

		return cluster.DeletePlacementRuleset(ctx, tx.Tx(), projectName, rulesetName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementRulesetDeleted.Event(projectName, rulesetName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/placement-rulesets/{name} placement-rulesets placement_ruleset_get
//
//	Get the placement ruleset
//
//	Gets a specific placement ruleset.
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
//	          $ref: "#/definitions/PlacementRuleset"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func placementRulesetGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	projectName := request.ProjectParam(r)
	rulesetName, err := url.PathUnescape(mux.Vars(r)["rulesetName"])
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypePlacementRuleset, false)
	if err != nil {
		return response.SmartError(err)
	}

	var dbRuleset *cluster.PlacementRuleset
	var usedBy []string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRuleset, err = cluster.GetPlacementRuleset(ctx, tx.Tx(), projectName, rulesetName)
		if err != nil {
			return err
		}

		usedBy, err = cluster.GetPlacementRulesetUsedBy(ctx, tx.Tx(), projectName, rulesetName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	ruleset := dbRuleset.ToAPI()
	etag := ruleset

	ruleset.UsedBy = project.FilterUsedBy(s.Authorizer, r, usedBy)
	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypePlacementRuleset, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.PlacementRulesetURL(projectName, rulesetName): &ruleset})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, ruleset, etag)
}

// swagger:operation PATCH /1.0/placement-rulesets/{name} placement-rulesets placement_ruleset_patch
//
//  Partially update the placement ruleset
//
//  Updates a subset of the placement ruleset.
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
//      name: placement ruleset
//      description: placement ruleset
//      required: true
//      schema:
//        $ref: "#/definitions/PlacementRulesetPut"
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

// swagger:operation PUT /1.0/placement-rulesets/{name} placement-rulesets placement_ruleset_put
//
//	Update the placement ruleset
//
//	Updates the entire placement ruleset.
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
//	    name: placement ruleset
//	    description: placement ruleset
//	    required: true
//	    schema:
//	      $ref: "#/definitions/PlacementRulesetPut"
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
func placementRulesetPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	projectName := request.ProjectParam(r)
	rulesetName, err := url.PathUnescape(mux.Vars(r)["rulesetName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.PlacementRulesetPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	var dbRuleset *cluster.PlacementRuleset
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRuleset, err = cluster.GetPlacementRuleset(ctx, tx.Tx(), projectName, rulesetName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	existing := dbRuleset.ToAPI()
	err = util.EtagCheck(r, existing)
	if err != nil {
		return response.SmartError(err)
	}

	switch r.Method {
	case http.MethodPut:
		existing.Description = req.Description
		existing.PlacementRules = req.PlacementRules
	case http.MethodPatch:
		if req.Description != "" {
			existing.Description = req.Description
		}

		for name, rule := range req.PlacementRules {
			existing.PlacementRules[name] = rule
		}
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		validatedRuleset, candidates, allMembers, err := placement.ValidateRuleset(ctx, tx, projectName, existing)
		if err != nil {
			return err
		}

		err = cluster.UpdatePlacementRuleset(ctx, tx.Tx(), projectName, rulesetName, *validatedRuleset)
		if err != nil {
			return err
		}

		nonConformantInstances, err := placement.GetNonConformantInstances(ctx, tx, rulesetName, projectName, candidates, allMembers)
		if err != nil {
			return err
		}

		for instanceID, member := range nonConformantInstances {
			err = tx.UpsertWarning(ctx, member.Name, projectName, entity.TypeInstance, instanceID, warningtype.InstanceNotConformantWithPlacementRuleset, "Move the instance to a member that conforms to the ruleset, or remove the ruleset from the instance configuration")
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementRulesetUpdated.Event(projectName, rulesetName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/placement-rulesets/{name} placement-rulesets placement_ruleset_post
//
//	Rename a placement ruleset
//
//	Renames the placement ruleset.
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
func placementRulesetPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	if !s.ServerClustered {
		return response.BadRequest(errors.New("This server is not clustered"))
	}

	projectName := request.ProjectParam(r)
	rulesetName, err := url.PathUnescape(mux.Vars(r)["rulesetName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.PlacementRulesetPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validate.IsDeviceName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.GetPlacementRulesetID(ctx, tx.Tx(), projectName, rulesetName)
		if err != nil {
			return err
		}

		usedBy, err := cluster.GetPlacementRulesetUsedBy(ctx, tx.Tx(), projectName, rulesetName)
		if err != nil {
			return err
		}

		if len(usedBy) > 0 {
			return api.StatusErrorf(http.StatusBadRequest, "Placement ruleset %q is currently in use", rulesetName)
		}

		return cluster.RenamePlacementRuleset(ctx, tx.Tx(), projectName, req.Name, rulesetName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.PlacementRulesetDeleted.Event(projectName, rulesetName, request.CreateRequestor(r), nil))

	return response.SyncResponseLocation(true, nil, api.NewURL().Project(projectName).Path("1.0", "placement-rulesets", req.Name).String())
}
