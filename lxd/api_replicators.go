package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
)

var replicatorsCmd = APIEndpoint{
	Path:            "replicators",
	MetricsType:     entity.TypeReplicator,
	ProjectSpecific: true,

	Get:  APIEndpointAction{Handler: replicatorsGet, AccessHandler: allowAuthenticated, AllProjectsMode: allProjectsModeDisallowRestrictedTLSClients},
	Post: APIEndpointAction{Handler: replicatorsPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateReplicators)},
}

var replicatorCmd = APIEndpoint{
	Path:            "replicators/{name}",
	MetricsType:     entity.TypeReplicator,
	ProjectSpecific: true,

	Get:    APIEndpointAction{Handler: replicatorGet, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: replicatorPost, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: replicatorPut, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: replicatorPatch, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: replicatorDelete, AccessHandler: allowPermission(entity.TypeReplicator, auth.EntitlementCanDelete, "name")},
}

var replicatorStateCmd = APIEndpoint{
	Path:            "replicators/{name}/state",
	MetricsType:     entity.TypeReplicator,
	ProjectSpecific: true,

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

		allConfigs, err := dbCluster.ReplicatorsConfigStore().GetAll(ctx, tx.Tx())
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

	name := r.PathValue("name")
	var apiReplicator *api.Replicator
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbReplicator, err := dbCluster.GetReplicator(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return fmt.Errorf("Failed loading replicator: %w", err)
		}

		config, err := dbCluster.ReplicatorsConfigStore().GetByEntityIDs(ctx, tx.Tx(), dbReplicator.Row.ID)
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
		// Required when creating a replicator.
		// ---
		//  type: string
		//  shortdesc: Target cluster link name.
		//  scope: global
		"cluster": validate.Required(func(value string) error {
			return validateReplicationClusterLink(ctx, s, value)
		}),

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

	// The validator loop only runs for keys present in config, so a missing "cluster" key
	// must be caught separately.
	if config["cluster"] == "" {
		return fmt.Errorf("Replicator configuration key %q is required", "cluster")
	}

	return nil
}

// replicatorCheckClusterLinkUnique returns an error if another replicator in the given project
// already targets the given cluster link. excludeID should be the ID of the replicator being
// updated, or 0 when creating a new replicator.
func replicatorCheckClusterLinkUnique(ctx context.Context, tx *db.ClusterTx, projectName string, clusterLinkName string, excludeID int64) error {
	replicators, _, err := dbCluster.GetReplicatorsAndURLs(ctx, tx.Tx(), &projectName, nil)
	if err != nil {
		return err
	}

	ids := make([]int64, 0, len(replicators))
	for _, replicator := range replicators {
		ids = append(ids, replicator.Row.ID)
	}

	allConfigs, err := dbCluster.ReplicatorsConfigStore().GetByEntityIDs(ctx, tx.Tx(), ids...)
	if err != nil {
		return fmt.Errorf("Failed loading replicator configs: %w", err)
	}

	for _, replicator := range replicators {
		if replicator.Row.ID == excludeID {
			continue
		}

		if allConfigs[replicator.Row.ID]["cluster"] == clusterLinkName {
			return api.StatusErrorf(http.StatusConflict, "A replicator targeting cluster link %q already exists in project %q", clusterLinkName, projectName)
		}
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

		err = replicatorCheckClusterLinkUnique(ctx, tx, projectName, req.Config["cluster"], 0)
		if err != nil {
			return err
		}

		id, err := dbCluster.CreateReplicator(ctx, tx.Tx(), dbCluster.ReplicatorRow{
			Name:        req.Name,
			Description: req.Description,
			ProjectID:   int64(dbProject.ID),
		})
		if err != nil {
			return fmt.Errorf("Failed creating replicator %q: %w", req.Name, err)
		}

		return dbCluster.ReplicatorsConfigStore().Set(ctx, tx.Tx(), id, req.Config)
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

	name := r.PathValue("name")
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

	name := r.PathValue("name")
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

		config, err := dbCluster.ReplicatorsConfigStore().GetByEntityIDs(ctx, tx.Tx(), dbReplicator.Row.ID)
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

	opArgs, err := prepareReplicatorRunOperation(r.Context(), s, projectName, name, clusterLinkName, restore, dbReplicator.Row.ID)
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

	return response.OperationResponse(op)
}

// updateReplicator is shared between [replicatorPut] and [replicatorPatch].
func updateReplicator(d *Daemon, r *http.Request, isPatch bool) response.Response {
	s := d.State()

	projectName, _, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	name := r.PathValue("name")
	var dbReplicator *dbCluster.Replicator
	var apiReplicator *api.Replicator
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbReplicator, err = dbCluster.GetReplicator(ctx, tx.Tx(), name, projectName)
		if err != nil {
			return fmt.Errorf("Failed loading replicator: %w", err)
		}

		config, err := dbCluster.ReplicatorsConfigStore().GetByEntityIDs(ctx, tx.Tx(), dbReplicator.Row.ID)
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

	if isPatch {
		for k, v := range apiReplicator.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	err = replicatorValidateConfig(r.Context(), s, req.Config)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err = replicatorCheckClusterLinkUnique(ctx, tx, projectName, req.Config["cluster"], dbReplicator.Row.ID)
		if err != nil {
			return err
		}

		if !isPatch || req.Description != "" {
			dbReplicator.Row.Description = req.Description
		}

		err = dbCluster.UpdateReplicator(ctx, tx.Tx(), dbReplicator.Row)
		if err != nil {
			return err
		}

		return dbCluster.ReplicatorsConfigStore().Set(ctx, tx.Tx(), dbReplicator.Row.ID, req.Config)
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

	name := r.PathValue("name")
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

	name := r.PathValue("name")
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
func prepareReplicatorRunOperation(ctx context.Context, s *state.State, projectName string, name string, clusterLinkName string, restore bool, replicatorID int64) (operations.OperationArgs, error) {
	// Load all DB state in a single transaction before any network I/O.
	var clusterLink *api.ClusterLink
	var targetCert *x509.Certificate
	var sourceProject *api.Project
	var allInsts []instance.Instance
	var nodeAddressByName map[string]string
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
		if err != nil {
			return err
		}

		// Load all instances in the project across all cluster members as
		// instance.Instance. instance.Load() only performs in-memory config
		// expansion and is safe for non-local instances.
		err = tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := instance.Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q: %w", dbInst.Name, err)
			}

			allInsts = append(allInsts, inst)
			return nil
		}, dbCluster.InstanceFilter{Project: &projectName})
		if err != nil {
			return fmt.Errorf("Failed listing project instances: %w", err)
		}

		// Pre-load node addresses for forwarding to other cluster members.
		nodes, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed listing cluster members: %w", err)
		}

		nodeAddressByName = make(map[string]string, len(nodes))
		for _, node := range nodes {
			nodeAddressByName[node.Name] = node.Address
		}

		return nil
	})
	if err != nil {
		return operations.OperationArgs{}, fmt.Errorf("Failed loading replicator state: %w", err)
	}

	clusterCert := s.Endpoints.NetworkCert()

	// Mutual TLS is safe to assume here: replicatorValidateConfig rejects public cluster links, which
	// are the only type that connects without presenting a client certificate.
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

	err = validateReplicatorModes(sourceProject.ReplicaMode, targetProject.ReplicaMode, restore)
	if err != nil {
		return operations.OperationArgs{}, api.StatusErrorf(http.StatusBadRequest, "%s", err)
	}

	targetCertPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: targetCert.Raw}))

	// In restore mode, all project instances across all cluster members must be stopped
	// before proceeding. The restore operation refreshes each existing instance from the
	// current leader cluster and creates any that only exist on the leader; a running instance
	// cannot be refreshed. Fail fast here to avoid a partial restore where some instances
	// are updated and others are not.
	if restore {
		err = replicatorCheckInstancesStopped(allInsts)
		if err != nil {
			return operations.OperationArgs{}, err
		}
	}

	// In restore mode the current leader cluster is the source of truth: use its instance list so
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
	}

	replicatorURL := entity.ReplicatorURL(projectName, name)
	projectURL := entity.ProjectURL(projectName)

	// Forward replication: iterate over all loaded instances directly.
	if !restore {
		return operations.OperationArgs{
			ProjectName:       projectName,
			EntityURL:         replicatorURL,
			Type:              operationtype.ReplicatorRun,
			Class:             operationtype.OperationClassTask,
			ConflictReference: replicatorURL.String(), // Prevents concurrent runs; paired with ConflictActionFail on the operation type to enforce cluster-wide exclusivity.
			Children:          buildForwardChildOps(s, projectName, replicatorURL, replicatorID, allInsts, nodeAddressByName, clusterLink, clusterCert, targetCert, targetCertPEM),
		}, nil
	}

	// Restore mode: iterate over the current leader cluster's instance list.
	return operations.OperationArgs{
		ProjectName:       projectName,
		EntityURL:         replicatorURL,
		Type:              operationtype.ReplicatorRun,
		Class:             operationtype.OperationClassTask,
		ConflictReference: replicatorURL.String(), // Prevents concurrent runs; paired with ConflictActionFail on the operation type to enforce cluster-wide exclusivity.
		Children:          buildRestoreChildOps(s, projectName, projectURL, replicatorURL, replicatorID, iterNames, allInsts, nodeAddressByName, clusterLink, clusterCert, targetCert),
	}, nil
}
