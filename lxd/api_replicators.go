package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/robfig/cron/v3"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
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
	"github.com/canonical/lxd/shared/validate"
)

var replicatorsCmd = APIEndpoint{
	Path:        "replicators",
	MetricsType: entity.TypeReplicator,

	Get:  APIEndpointAction{Handler: replicatorsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: replicatorsPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateReplicators)},
}

var replicatorCmd = APIEndpoint{
	Path:        "replicators/{name}",
	MetricsType: entity.TypeReplicator,

	Get:    APIEndpointAction{Handler: replicatorGet, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: replicatorPost, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: replicatorPut, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: replicatorPatch, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: replicatorDelete, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanDelete, "name")},
}

var replicatorStateCmd = APIEndpoint{
	Path:        "replicators/{name}/state",
	MetricsType: entity.TypeReplicator,

	Get: APIEndpointAction{Handler: replicatorStateGet, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanView, "name")},
	Put: APIEndpointAction{Handler: replicatorStatePut, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
}

// swagger:operation GET /1.0/replicators replicators replicators_get
//
//	Get the replicators
//
//	Returns a list of replicators (URLs).
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
//	    description: Retrieve replicators from all projects
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
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/replicators/foo?project=default",
//	              "/1.0/replicators/bar?project=default"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/replicators?recursion=1 replicators replicators_get_recursion1
//
//	Get the replicators
//
//	Returns a list of replicators (structs).
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
//	    description: Retrieve replicators from all projects
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
//	          description: List of replicators
//	          items:
//	            $ref: "#/definitions/Replicator"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func replicatorsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	recursion, _ := util.IsRecursionRequest(r)

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeReplicator, true)
	if err != nil {
		return response.SmartError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeReplicator)
	if err != nil {
		return response.SmartError(err)
	}

	var projectFilter *string
	if !allProjects {
		projectFilter = &projectName
	}

	var apiReplicators []*api.Replicator
	var replicatorURLs []string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		replicators, urls, err := dbCluster.GetReplicatorsAndURLs(ctx, tx.Tx(), projectFilter, func(replicator dbCluster.Replicator) bool {
			return userHasPermission(entity.ReplicatorURL(replicator.ProjectName, replicator.Row.Name))
		})
		if err != nil {
			return err
		}

		replicatorURLs = urls
		if recursion == 0 {
			return nil
		}

		allConfigs, err := dbCluster.GetReplicatorConfig(ctx, tx.Tx(), nil)
		if err != nil {
			return fmt.Errorf("Failed loading replicator configs: %w", err)
		}

		apiReplicatorsTx := make([]*api.Replicator, 0, len(replicators))
		for _, replicator := range replicators {
			apiReplicatorsTx = append(apiReplicatorsTx, replicator.ToAPI(allConfigs))
		}

		apiReplicators = apiReplicatorsTx
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, replicatorURLs)
	}

	if len(withEntitlements) > 0 {
		urlToReplicator := make(map[*api.URL]auth.EntitlementReporter, len(apiReplicators))
		for _, replicator := range apiReplicators {
			urlToReplicator[entity.ReplicatorURL(replicator.Project, replicator.Name)] = replicator
		}

		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeReplicator, withEntitlements, urlToReplicator)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, apiReplicators)
}

// swagger:operation GET /1.0/replicators/{name} replicators replicator_get
//
//	Get the replicator
//
//	Gets a specific replicator.
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
//	    description: Replicator
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
//	          $ref: "#/definitions/Replicator"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func replicatorGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeReplicator, false)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var apiReplicator *api.Replicator
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbReplicator, err := dbCluster.GetReplicator(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return fmt.Errorf("Failed loading replicator: %w", err)
		}

		config, err := dbCluster.GetReplicatorConfig(ctx, tx.Tx(), &dbReplicator.Row.ID)
		if err != nil {
			return fmt.Errorf("Failed loading replicator config: %w", err)
		}

		apiReplicator = dbReplicator.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeReplicator, withEntitlements, map[*api.URL]auth.EntitlementReporter{
			entity.ReplicatorURL(projectName, name): apiReplicator,
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, apiReplicator, apiReplicator.Writable())
}

// replicatorValidateConfig validates replicator configuration keys and values.
// It also checks that the caller has permission to view any referenced cluster link.
func replicatorValidateConfig(ctx context.Context, s *state.State, config map[string]string) error {
	replicatorConfigKeys := map[string]func(value string) error{
		// lxdmeta:generate(entities=replicator; group=conf; key=cluster)
		// Required when creating a replicator. When updating, this key can be omitted to keep the existing cluster link.
		// ---
		//  type: string
		//  shortdesc: Target cluster link name.
		//  scope: global
		"cluster": validate.Optional(func(value string) error {
			err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := dbCluster.GetClusterLink(ctx, tx.Tx(), value)
				if err != nil {
					if api.StatusErrorCheck(err, http.StatusNotFound) {
						return api.StatusErrorf(http.StatusNotFound, "Cluster link %q not found", value)
					}

					return err
				}

				return nil
			})
			if err != nil {
				return err
			}

			return s.Authorizer.CheckPermission(ctx, entity.ClusterLinkURL(value), auth.EntitlementCanView)
		}),

		// lxdmeta:generate(entities=replicator; group=conf; key=snapshot)
		//
		// ---
		//  type: bool
		//  shortdesc: Whether to snapshot instances before replication.
		//  scope: global
		"snapshot": validate.Optional(validate.IsBool),

		// lxdmeta:generate(entities=replicator; group=conf; key=schedule)
		// Specify a cron expression for the replication schedule. For example, `@daily` or `0 6 * * *`.
		// ---
		//  type: string
		//  shortdesc: Cron expression for the replication schedule.
		//  scope: global
		"schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly"})),
	}

	for k, v := range config {
		// lxdmeta:generate(entities=replicator; group=miscellaneous; key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := replicatorConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid replicator configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid value for replicator configuration key %q: %w", k, err)
		}
	}

	if config["cluster"] == "" {
		return fmt.Errorf("Replicator configuration key %q is required", "cluster")
	}

	return nil
}

// swagger:operation POST /1.0/replicators replicators replicators_post
//
//	Add a replicator
//
//	Creates a new replicator.
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
//	    name: replicator
//	    description: Replicator
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ReplicatorsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func replicatorsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ReplicatorsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validate.IsDeviceName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = replicatorValidateConfig(r.Context(), s, req.Config)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed loading project %q: %w", projectName, err)
		}

		id, err := dbCluster.CreateReplicator(ctx, tx.Tx(), dbCluster.ReplicatorRow{
			Name:        req.Name,
			Description: req.Description,
			ProjectID:   int64(dbProject.ID),
		})
		if err != nil {
			return fmt.Errorf("Failed creating replicator %q: %w", req.Name, err)
		}

		return dbCluster.CreateReplicatorConfig(ctx, tx.Tx(), id, req.Config)
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorCreated.Event(r.Context(), req.Name, projectName, nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/replicators/{name} replicators replicator_post
//
//	Rename the replicator
//
//	Renames the replicator.
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
//	    name: replicator
//	    description: Replicator rename options
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ReplicatorPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func replicatorPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ReplicatorPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = validate.IsDeviceName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.RenameReplicator(ctx, tx.Tx(), name, projectName, req.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorRenamed.Event(r.Context(), req.Name, projectName, logger.Ctx{"old_name": name}))

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/replicators/{name}/state replicators replicator_state_put
//
//	Update the replicator state
//
//	Triggers a replicator run using the specified action.
//	The "restore" action requires all local project instances to be stopped;
//	it returns 400 if any instance is running to prevent partial restores.
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
//	    name: state
//	    description: Replicator state
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ReplicatorStatePut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func replicatorStatePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ReplicatorStatePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	switch req.Action {
	case "start", "restore":
	default:
		return response.BadRequest(fmt.Errorf("Unknown action %q", req.Action))
	}

	restore := req.Action == "restore"

	var dbReplicator *dbCluster.Replicator
	var apiReplicator *api.Replicator
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		dbReplicator, err = dbCluster.GetReplicator(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return err
		}

		config, err := dbCluster.GetReplicatorConfig(ctx, tx.Tx(), &dbReplicator.Row.ID)
		if err != nil {
			return fmt.Errorf("Failed loading replicator config: %w", err)
		}

		apiReplicator = dbReplicator.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	clusterLinkName := apiReplicator.Config["cluster"]
	if clusterLinkName == "" {
		return response.BadRequest(fmt.Errorf("Replicator %q has no cluster link configured", name))
	}

	opArgs, err := prepareReplicatorRunOperation(r.Context(), s, projectName, name, clusterLinkName, restore, dbReplicator.Row.ID, shared.IsTrue(apiReplicator.Config["snapshot"]))
	if err != nil {
		return response.SmartError(err)
	}

	// Set status to Running before scheduling the operation. The operation's RunHook writes
	// the terminal status (Completed/Failed) when it finishes. If the project has no instances,
	// the RunHook can complete synchronously inside ScheduleUserOperationFromRequest before it
	// returns, writing the terminal status first. By setting Running here, that terminal write
	// always comes after Running, so the status is never left stuck at Running.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.UpdateReplicatorLastRun(ctx, tx.Tx(), dbReplicator.Row.ID, time.Now(), api.ReplicatorStatusRunning)
	})
	if err != nil {
		logger.Warn("Failed updating replicator last run status to running", logger.Ctx{"name": name, "project": projectName, "err": err})
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, opArgs)
	if err != nil {
		// Revert Running to Failed so the status doesn't get stuck.
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.UpdateReplicatorLastRunStatus(ctx, tx.Tx(), dbReplicator.Row.ID, api.ReplicatorStatusFailed)
		})

		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorRun.Event(r.Context(), name, projectName, nil))

	return operations.OperationResponse(op)
}

// updateReplicator is shared between [replicatorPut] and [replicatorPatch].
func updateReplicator(d *Daemon, r *http.Request, isPatch bool) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbReplicator *dbCluster.Replicator
	var apiReplicator *api.Replicator
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbReplicator, err = dbCluster.GetReplicator(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return fmt.Errorf("Failed loading replicator: %w", err)
		}

		config, err := dbCluster.GetReplicatorConfig(ctx, tx.Tx(), &dbReplicator.Row.ID)
		if err != nil {
			return fmt.Errorf("Failed loading replicator config: %w", err)
		}

		apiReplicator = dbReplicator.ToAPI(config)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = util.EtagCheck(r, apiReplicator.Writable())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ReplicatorPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range apiReplicator.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	err = replicatorValidateConfig(r.Context(), s, req.Config)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if !isPatch || req.Description != "" {
			dbReplicator.Row.Description = req.Description
		}

		err = dbCluster.UpdateReplicator(ctx, tx.Tx(), dbReplicator.Row)
		if err != nil {
			return err
		}

		return dbCluster.UpdateReplicatorConfig(ctx, tx.Tx(), dbReplicator.Row.ID, req.Config)
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorUpdated.Event(r.Context(), name, projectName, nil))

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/replicators/{name} replicators replicator_put
//
//	Update the replicator
//
//	Updates the replicator configuration.
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
//	    name: replicator
//	    description: Replicator configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ReplicatorPut"
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
func replicatorPut(d *Daemon, r *http.Request) response.Response {
	return updateReplicator(d, r, false)
}

// swagger:operation PATCH /1.0/replicators/{name} replicators replicator_patch
//
//	Partially update the replicator
//
//	Updates a subset of the replicator configuration.
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
//	    name: replicator
//	    description: Replicator configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ReplicatorPut"
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
func replicatorPatch(d *Daemon, r *http.Request) response.Response {
	return updateReplicator(d, r, true)
}

// swagger:operation DELETE /1.0/replicators/{name} replicators replicator_delete
//
//	Delete the replicator
//
//	Deletes the replicator.
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
func replicatorDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteReplicator(ctx, tx.Tx(), name, projectName)
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting replicator %q: %w", name, err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.ReplicatorDeleted.Event(r.Context(), name, projectName, nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/replicators/{name}/state replicators replicator_state_get
//
//	Get the replicator state
//
//	Gets the current state of the replicator.
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
//	    description: Replicator state
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
//	          $ref: "#/definitions/ReplicatorState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func replicatorStateGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var status string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbReplicator, err := dbCluster.GetReplicator(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return err
		}

		status = api.ReplicatorStatusPending
		if dbReplicator.Row.LastRunStatus != "" {
			status = dbReplicator.Row.LastRunStatus
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading replicator state for %q: %w", name, err))
	}

	return response.SyncResponse(true, api.ReplicatorState{Status: status})
}

// runScheduledReplicatorsTask returns a background task that checks replicator schedules every minute
// and triggers replication for any replicator whose cron expression matches the current time.
func runScheduledReplicatorsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		err := runScheduledReplicators(ctx, stateFunc())
		if err != nil {
			logger.Error("Failed running scheduled replicator task", logger.Ctx{"err": err})
		}
	}

	first := true
	schedule := func() (time.Duration, error) {
		// Skip the first run to avoid triggering replicators immediately at daemon
		// startup if the start time happens to coincide with a scheduled minute.
		if first {
			first = false
			return time.Minute, task.ErrSkip
		}

		return time.Minute, nil
	}

	return f, schedule
}

// prepareReplicatorRunOperation builds the operation used to run a replicator.
func prepareReplicatorRunOperation(ctx context.Context, s *state.State, projectName string, name string, clusterLinkName string, restore bool, replicatorID int64, snapshot bool) (operations.OperationArgs, error) {
	// Load all DB state in a single transaction before any network I/O.
	var clusterLink *api.ClusterLink
	var targetCert *x509.Certificate
	var sourceProject *api.Project
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		_, clusterLink, targetCert, err = lxdCluster.LoadClusterLinkAndCert(ctx, tx.Tx(), clusterLinkName)
		if err != nil {
			return err
		}

		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		sourceProject, err = dbProject.ToAPI(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return operations.OperationArgs{}, fmt.Errorf("Failed loading replicator run state: %w", err)
	}

	clusterCert, err := util.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return operations.OperationArgs{}, fmt.Errorf("Failed loading cluster certificate: %w", err)
	}

	connArgs := lxdCluster.GetClusterLinkConnectionArgs(clusterCert, targetCert)
	targetClient, err := lxdCluster.ConnectCluster(ctx, *clusterLink, connArgs)
	if err != nil {
		return operations.OperationArgs{}, fmt.Errorf("Failed connecting to target cluster: %w", err)
	}

	targetClient = targetClient.UseProject(projectName)

	targetProject, _, err := targetClient.GetProject(projectName)
	if err != nil {
		return operations.OperationArgs{}, fmt.Errorf("Failed getting target project: %w", err)
	}

	err = validateReplicatorModes(sourceProject.Config["replica.mode"], targetProject.Config["replica.mode"], restore)
	if err != nil {
		return operations.OperationArgs{}, api.StatusErrorf(http.StatusBadRequest, "%s", err)
	}

	targetCertPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

	// Load local instances for forward replication and for restore of pre-existing instances.
	localInsts, err := instanceLoadNodeProjectAll(ctx, s, projectName, instancetype.Any)
	if err != nil {
		return operations.OperationArgs{}, fmt.Errorf("Failed listing instances: %w", err)
	}

	localInstsByName := make(map[string]instance.Instance, len(localInsts))
	for _, inst := range localInsts {
		localInstsByName[inst.Name()] = inst
	}

	// In restore mode, all local instances must be stopped before proceeding.
	// The restore operation refreshes each existing local instance from the remote leader
	// and creates any that only exist on the leader; a running instance cannot be refreshed.
	// Fail fast here to avoid a partial restore where some instances are updated and
	// others are not.
	if restore {
		for _, inst := range localInsts {
			if inst.IsRunning() {
				return operations.OperationArgs{}, fmt.Errorf("Instance %q is running, stop all project instances before running --restore", inst.Name())
			}
		}
	}

	// In restore mode the remote leader is the source of truth: use its instance list so
	// that instances created on the leader after failover are included. Restore is additive
	// only: local instances that do not exist on the leader are left in place and not deleted.
	var iterNames []string
	if restore {
		remoteInsts, err := targetClient.GetInstances(lxd.GetInstancesArgs{InstanceType: api.InstanceTypeAny})
		if err != nil {
			return operations.OperationArgs{}, fmt.Errorf("Failed listing instances on target: %w", err)
		}

		iterNames = make([]string, 0, len(remoteInsts))
		for _, ri := range remoteInsts {
			iterNames = append(iterNames, ri.Name)
		}
	} else {
		iterNames = make([]string, 0, len(localInsts))
		for _, inst := range localInsts {
			iterNames = append(iterNames, inst.Name())
		}
	}

	replicatorURL := entity.ReplicatorURL(projectName, name)
	projectURL := entity.ProjectURL(projectName)
	childArgs := make([]*operations.OperationArgs, 0, len(iterNames))

	for _, instName := range iterNames {
		inst := localInstsByName[instName] // nil for instances that only exist on the remote leader

		copyFunc := func(ctx context.Context, op *operations.Operation) error {
			dstClient, err := lxdCluster.ConnectCluster(ctx, *clusterLink, lxdCluster.GetClusterLinkConnectionArgs(clusterCert, targetCert))
			if err != nil {
				return fmt.Errorf("Failed connecting to target cluster: %w", err)
			}

			dstClient = dstClient.UseProject(projectName)

			if restore {
				// In restore mode the local copy is stale; fetch current metadata from
				// the remote leader so the restore uses up-to-date config/state.
				freshInst, _, err := dstClient.GetInstance(instName)
				if err != nil {
					if api.StatusErrorCheck(err, http.StatusNotFound) {
						// Instance was deleted on the leader after failover; skip it rather
						// than failing the whole run, since the deletion is intentional.
						logger.Warn("Skipping restore of instance deleted on leader", logger.Ctx{"instance": instName})
						return nil
					}

					return fmt.Errorf("Failed getting instance %q from remote: %w", instName, err)
				}

				// Only create a snapshot if the instance has no existing snapshot schedule.
				// When a schedule is set, the most recent existing snapshot is reused.
				if snapshot && freshInst.ExpandedConfig["snapshots.schedule"] == "" {
					snapOp, err := dstClient.CreateInstanceSnapshot(instName, api.InstanceSnapshotsPost{})
					if err != nil {
						return fmt.Errorf("Failed creating snapshot of instance %q: %w", instName, err)
					}

					err = snapOp.Wait()
					if err != nil {
						return fmt.Errorf("Failed waiting for snapshot of instance %q: %w", instName, err)
					}
				}

				remoteMigrateOp, err := dstClient.MigrateInstance(instName, api.InstancePost{Migration: true})
				if err != nil {
					return fmt.Errorf("Failed starting migration source on remote for instance %q: %w", instName, err)
				}

				remoteOpInfo := remoteMigrateOp.Get()
				remoteSecrets, err := remoteOpInfo.WebsocketSecrets()
				if err != nil {
					return fmt.Errorf("Failed getting websocket secrets for instance %q migration: %w", instName, err)
				}

				remoteAddresses := shared.SplitNTrimSpace(clusterLink.Config["volatile.addresses"], ",", -1, false)
				if len(remoteAddresses) == 0 {
					return fmt.Errorf("No remote addresses available for cluster link %q", clusterLinkName)
				}

				remoteOpURL := "https://" + remoteAddresses[0] + "/1.0/operations/" + remoteOpInfo.ID

				// Load profiles for the instance to pass to the migration sink.
				var profiles []api.Profile
				err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					profiles, err = instanceProfilesFromNames(ctx, tx, projectName, freshInst.Profiles)
					return err
				})
				if err != nil {
					return fmt.Errorf("Failed loading profiles for instance %q: %w", instName, err)
				}

				// Build a migration request that the shared migration-sink core understands.
				migrateReq := &api.InstancesPost{
					InstancePut: api.InstancePut{
						Architecture: freshInst.Architecture,
						Config:       freshInst.Config,
						Devices:      freshInst.Devices,
						Description:  freshInst.Description,
						Ephemeral:    freshInst.Ephemeral,
						Profiles:     freshInst.Profiles,
						Stateful:     freshInst.Stateful,
					},
					Name: instName,
					Type: api.InstanceType(freshInst.Type),
					Source: api.InstanceSource{
						Type:        api.SourceTypeMigration,
						Mode:        "pull",
						Operation:   remoteOpURL,
						Websockets:  remoteSecrets,
						Certificate: targetCertPEM,
						Refresh:     inst != nil,
					},
				}

				result, err := prepareInstanceMigrationSink(ctx, s, projectName, profiles, migrateReq, "")
				if err != nil {
					return fmt.Errorf("Failed preparing migration sink for instance %q: %w", instName, err)
				}

				defer result.revert.Fail()

				err = result.run(ctx, op)
				if err != nil {
					return fmt.Errorf("Restore of instance %q failed: %w", instName, err)
				}

				result.revert.Success()
				return remoteMigrateOp.Wait()
			}

			if snapshot && inst.ExpandedConfig()["snapshots.schedule"] == "" {
				snapName, err := instance.NextSnapshotName(s, inst, "snap%d")
				if err != nil {
					return fmt.Errorf("Failed generating snapshot name for instance %q: %w", instName, err)
				}

				err = inst.Snapshot(ctx, snapName, nil, false, api.DiskVolumesModeRoot, nil)
				if err != nil {
					return fmt.Errorf("Failed creating snapshot of instance %q: %w", instName, err)
				}
			}

			srcRenderRes, _, err := inst.Render()
			if err != nil {
				return fmt.Errorf("Failed rendering source instance %q: %w", instName, err)
			}

			srcInstInfo, ok := srcRenderRes.(*api.Instance)
			if !ok {
				return fmt.Errorf("Unexpected result from source instance render for %q", instName)
			}

			// Set up a push-mode migration sink on the destination. In push mode the
			// leader (source) connects outward to the destination, so the destination
			// does not need to reach back into the leader. This is required when the
			// destination project is restricted, which disallows pull-mode migrations.
			destOp, err := dstClient.CreateInstance(api.InstancesPost{
				Name:        instName,
				InstancePut: srcInstInfo.Writable(),
				Type:        api.InstanceType(srcInstInfo.Type),
				Source: api.InstanceSource{
					Type:    api.SourceTypeMigration,
					Mode:    "push",
					Refresh: true,
				},
			})
			if err != nil {
				return fmt.Errorf("Failed requesting instance create on destination: %w", err)
			}

			// Guard against leaving the destination sink operation running if we fail
			// before starting the source; disarmed once the source op is scheduled.
			destOpCancelled := false
			defer func() {
				if !destOpCancelled {
					_ = destOp.Cancel()
				}
			}()

			destOpAPI := destOp.Get()
			destSecrets, err := destOpAPI.WebsocketSecrets()
			if err != nil {
				return fmt.Errorf("Failed getting websocket secrets from destination for instance %q: %w", instName, err)
			}

			// ConnectCluster tries each configured address in order; GetConnectionInfo
			// returns the one that succeeded, which is what the push target must use.
			dstConnInfo, err := dstClient.GetConnectionInfo()
			if err != nil {
				return fmt.Errorf("Failed getting connection info for destination: %w", err)
			}

			pushTarget := &api.InstancePostTarget{
				Operation:   dstConnInfo.URL + "/1.0/operations/" + destOpAPI.ID,
				Websockets:  destSecrets,
				Certificate: targetCertPEM,
			}

			srcMigration, err := newMigrationSource(inst, false, false, false, "", pushTarget)
			if err != nil {
				return fmt.Errorf("Failed setting up migration source for instance %q: %w", instName, err)
			}

			migrArgs := operations.OperationArgs{
				ProjectName: projectName,
				EntityURL:   entity.InstanceURL(projectName, instName),
				Type:        operationtype.InstanceMigrate,
				Class:       operations.OperationClassTask,
				RunHook: func(ctx context.Context, innerOp *operations.Operation) error {
					done := make(chan struct{})
					defer close(done)
					go func() {
						select {
						case <-done:
						case <-ctx.Done():
							srcMigration.disconnect()
						}
					}()

					return srcMigration.Do(ctx, s, innerOp)
				},
			}

			srcOp, err := func() (*operations.Operation, error) {
				if op.Requestor() != nil {
					return operations.ScheduleUserOperationFromOperation(s, op, migrArgs)
				}

				return operations.ScheduleServerOperation(s, migrArgs)
			}()
			if err != nil {
				return err
			}

			destOpCancelled = true // source is now connected via websockets; cancel would interrupt an in-flight transfer

			err = srcOp.Wait(context.Background())
			if err != nil {
				return fmt.Errorf("Replication of instance %q failed on source: %w", instName, err)
			}

			return destOp.Wait()
		}

		childArgs = append(childArgs, &operations.OperationArgs{
			ProjectName: projectName,
			EntityURL:   projectURL,
			Type:        operationtype.ReplicatorRunInstance,
			Class:       operations.OperationClassTask,
			Metadata: map[string]any{
				api.MetadataEntityURL: entity.InstanceURL(projectName, instName).String(),
			},
			RunHook: copyFunc,
		})
	}

	return operations.OperationArgs{
		ProjectName:       projectName,
		EntityURL:         replicatorURL,
		Type:              operationtype.ReplicatorRun,
		Class:             operations.OperationClassTask,
		ConflictReference: replicatorURL.String(), // Prevents concurrent runs; paired with ConflictActionFail on the operation type to enforce cluster-wide exclusivity.
		Children:          childArgs,
		RunHook: func(_ context.Context, op *operations.Operation) error {
			runStatus := api.ReplicatorStatusCompleted
			for _, child := range op.Children() {
				if child.Status() != api.Success {
					runStatus = api.ReplicatorStatusFailed
					break
				}
			}

			// Use a fresh context so the status write always completes, even if the operation context was cancelled.
			// Only the status is updated here; last_run_date was already set when the operation started.
			return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return dbCluster.UpdateReplicatorLastRunStatus(ctx, tx.Tx(), replicatorID, runStatus)
			})
		},
	}, nil
}

// runScheduledReplicators loads all replicators, checks their schedule config key against the current
// time, and triggers replication for those that are due.
func runScheduledReplicators(ctx context.Context, s *state.State) error {
	// Load all replicators across all projects.
	var apiReplicators []*api.Replicator
	var replicatorRows []dbCluster.Replicator
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		replicators, _, err := dbCluster.GetReplicatorsAndURLs(ctx, tx.Tx(), nil, func(_ dbCluster.Replicator) bool { return true })
		if err != nil {
			return fmt.Errorf("Failed loading replicators: %w", err)
		}

		allConfigs, err := dbCluster.GetReplicatorConfig(ctx, tx.Tx(), nil)
		if err != nil {
			return fmt.Errorf("Failed loading replicator configs: %w", err)
		}

		apiReplicatorsTx := make([]*api.Replicator, 0, len(replicators))
		for _, replicator := range replicators {
			apiReplicatorsTx = append(apiReplicatorsTx, replicator.ToAPI(allConfigs))
		}

		apiReplicators = apiReplicatorsTx
		replicatorRows = replicators
		return nil
	})
	if err != nil {
		return err
	}

	// Build a per-project replica.mode map so the loop can skip standby projects
	// without an extra DB round-trip per replicator.
	projectModes := make(map[string]string, len(apiReplicators))
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, replicator := range apiReplicators {
			_, ok := projectModes[replicator.Project]
			if ok {
				continue
			}

			config, err := dbCluster.GetProjectConfig(ctx, tx.Tx(), replicator.Project)
			if err != nil {
				return fmt.Errorf("Failed loading project config for %q: %w", replicator.Project, err)
			}

			projectModes[replicator.Project] = config["replica.mode"]
		}

		return nil
	})
	if err != nil {
		return err
	}

	now := time.Now()
	for i, replicator := range apiReplicators {
		if projectModes[replicator.Project] != api.ReplicatorProjectModeLeader {
			continue
		}

		schedule, ok := replicator.Config["schedule"]
		if !ok || schedule == "" {
			continue
		}

		if !replicatorIsScheduledNow(schedule, now) {
			continue
		}

		row := &replicatorRows[i]
		logger.Debug("Running scheduled replicator", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project, "schedule": schedule})

		err := triggerScheduledReplicator(ctx, s, replicator, row)
		if err != nil {
			logger.Error("Failed running scheduled replicator", logger.Ctx{
				"replicator": replicator.Name,
				"project":    replicator.Project,
				"err":        err,
			})
		}
	}

	return nil
}

// replicatorIsScheduledNow returns true if any of the (comma-separated) cron expressions in spec matches the provided minute.
func replicatorIsScheduledNow(spec string, now time.Time) bool {
	t := now.Truncate(time.Minute)
	// Split on ", " (comma+space) to match validate.IsCron, preserving intra-field commas like "0,30 * * * *".
	for _, s := range shared.SplitNTrimSpace(spec, ", ", -1, true) {
		sched, err := cron.ParseStandard(s)
		if err != nil {
			logger.Warn("Failed parsing replicator schedule expression", logger.Ctx{"spec": s, "err": err})
			continue
		}

		// Next(t - 1s) returns the next scheduled time strictly after t-1s.
		// If t itself is a scheduled minute, that equals t.
		if sched.Next(t.Add(-time.Second)).Equal(t) {
			return true
		}
	}

	return false
}

// triggerScheduledReplicator runs replication for a single replicator as a background server operation.
// It blocks until the operation completes so that last_run_date is persisted before the next scheduler
// tick and operation results are visible to callers.
func triggerScheduledReplicator(ctx context.Context, s *state.State, replicator *api.Replicator, row *dbCluster.Replicator) error {
	clusterLinkName := replicator.Config["cluster"]
	if clusterLinkName == "" {
		return fmt.Errorf("Replicator %q has no cluster link configured", replicator.Name)
	}

	opArgs, err := prepareReplicatorRunOperation(ctx, s, replicator.Project, replicator.Name, clusterLinkName, false, row.Row.ID, shared.IsTrue(replicator.Config["snapshot"]))
	if err != nil {
		return err
	}

	// Set status to Running before scheduling the operation. The operation's RunHook writes
	// the terminal status (Completed/Failed) when it finishes. If the project has no instances,
	// the RunHook can complete synchronously inside ScheduleServerOperation before it returns,
	// writing the terminal status first. By setting Running here, that terminal write always
	// comes after Running, so the status is never left stuck at Running.
	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.UpdateReplicatorLastRun(ctx, tx.Tx(), row.Row.ID, time.Now(), api.ReplicatorStatusRunning)
	})
	if err != nil {
		logger.Warn("Failed updating replicator last run status to running", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project, "err": err})
	}

	op, err := operations.ScheduleServerOperation(s, opArgs)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			logger.Warn("Skipping scheduled replicator, a run is already in progress", logger.Ctx{"replicator": replicator.Name, "project": replicator.Project})
			// Don't revert Running: another operation is in progress and owns the status;
			// it will write its own terminal state when it completes.
			return nil
		}

		// Revert Running to Failed so the status doesn't get stuck.
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.UpdateReplicatorLastRunStatus(ctx, tx.Tx(), row.Row.ID, api.ReplicatorStatusFailed)
		})

		return fmt.Errorf("Failed scheduling replicator operation: %w", err)
	}

	return op.Wait(ctx)
}

// validateReplicatorModes checks the source and target replica modes for a run.
func validateReplicatorModes(sourceMode string, targetMode string, restore bool) error {
	if restore {
		if sourceMode != api.ReplicatorProjectModeStandby {
			return errors.New(`Source project must have "replica.mode" set to standby to run replicator in restore mode`)
		}

		if targetMode != api.ReplicatorProjectModeLeader {
			return errors.New(`Target project must have "replica.mode" set to leader to run replicator in restore mode`)
		}
	} else {
		if sourceMode != api.ReplicatorProjectModeLeader {
			return errors.New(`Source project must have "replica.mode" set to leader to run replicator`)
		}

		if targetMode != api.ReplicatorProjectModeStandby {
			return errors.New(`Target project must have "replica.mode" set to standby to run replicator`)
		}
	}

	return nil
}
