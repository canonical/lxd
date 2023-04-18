package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/deployments"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var deploymentsCmd = APIEndpoint{
	Path: "deployments",

	Get:  APIEndpointAction{Handler: deploymentsGet, AccessHandler: allowProjectPermission("deployments", "view")},
	Post: APIEndpointAction{Handler: deploymentsPost, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
}

var deploymentCmd = APIEndpoint{
	Path: "deployments/{deploymentName}",

	Delete: APIEndpointAction{Handler: deploymentDelete, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
	Get:    APIEndpointAction{Handler: deploymentGet, AccessHandler: allowProjectPermission("deployments", "view")},
	Put:    APIEndpointAction{Handler: deploymentPut, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
	Patch:  APIEndpointAction{Handler: deploymentPut, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
	Post:   APIEndpointAction{Handler: deploymentPost, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
}

var deploymentInstanceSetsCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/instance-sets",

	Get:  APIEndpointAction{Handler: deploymentInstanceSetsGet, AccessHandler: allowProjectPermission("deployments", "view")},
	Post: APIEndpointAction{Handler: deploymentInstanceSetsPost, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
}

var deploymentInstanceSetCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/instance-sets/{instSetName}",

	Delete: APIEndpointAction{Handler: deploymentInstanceSetDelete, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
	Get:    APIEndpointAction{Handler: deploymentInstanceSetGet, AccessHandler: allowProjectPermission("deployments", "view")},
	Put:    APIEndpointAction{Handler: deploymentInstanceSetPut, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
	Patch:  APIEndpointAction{Handler: deploymentInstanceSetPut, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
	Post:   APIEndpointAction{Handler: deploymentInstanceSetPost, AccessHandler: allowProjectPermission("deployments", "manage-deployments")},
}

// API endpoints.

// swagger:operation GET /1.0/deployments deployments deployments_get
//
//  Get the deployments
//
//  Returns a list of deployments (URLs).
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
//                "/1.0/deployments/foo",
//                "/1.0/deployments/bar"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/deployments?recursion=1 deployments deployments_get_recursion1
//
//	Get the deployments
//
//	Returns a list of deployments (structs).
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
//	    description: Deployments
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
//	          description: List of deployments
//	          items:
//	            $ref: "#/definitions/Deployment"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	var err error
	var dbDeployments []*db.Deployment

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filters := []db.DeploymentFilter{{
			Project: &projectName,
		}}

		dbDeployments, err = tx.GetDeployments(ctx, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading deployments: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Sort by deployments name.
	sort.SliceStable(dbDeployments, func(i, j int) bool {
		deploymentA := dbDeployments[i]
		deploymentB := dbDeployments[j]

		return deploymentA.Name < deploymentB.Name
	})

	if util.IsRecursionRequest(r) {
		deployments := make([]*api.Deployment, 0, len(dbDeployments))
		for _, dbDeployment := range dbDeployments {
			deployments = append(deployments, &dbDeployment.Deployment)
		}

		// tomp TODO populated UsedBy.

		return response.SyncResponse(true, deployments)
	}

	urls := make([]string, 0, len(dbDeployments))
	for _, dbDeployment := range dbDeployments {
		urls = append(urls, dbDeployment.Deployment.URL(version.APIVersion, projectName).String())
	}

	return response.SyncResponse(true, urls)
}

// swagger:operation POST /1.0/deployments deployments deployments_post
//
//	Add a deployment
//
//	Creates a new deployment.
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
//	    name: deployment
//	    description: Deployment
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	req := api.DeploymentsPost{}

	// Parse the request into a record.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	_, err = deployments.LoadByName(s, projectName, req.Name)
	if err == nil {
		return response.BadRequest(fmt.Errorf("The deployment already exists"))
	}

	err = deployments.Create(s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	lc := lifecycle.DeploymentCreated.Event(projectName, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/deployments/{name} deployments deployments_delete
//
//	Delete the deployment
//
//	Removes the deloyment.
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
func deploymentDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	err = deployment.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.DeploymentDeleted.Event(projectName, deploymentName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/deployments/{name} deployments deployments_get
//
//	Get the deployment
//
//	Gets a specific deployment.
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
//	    description: Deployment
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
//	          $ref: "#/definitions/Deployment"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeployment *db.Deployment
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err = tx.GetDeployment(ctx, projectName, deploymentName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, dbDeployment.Deployment, dbDeployment.Deployment.Etag())
}

// swagger:operation PATCH /1.0/deployments/{name} deployments deployments_patch
//
//  Partially update the deployment
//
//  Updates a subset of the deployment configuration.
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
//      name: deployment
//      description: Deployment configuration
//      required: true
//      schema:
//        $ref: "#/definitions/DeploymentPut"
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

// swagger:operation PUT /1.0/deployments/{name} deployments deployments_put
//
//	Update the deployment
//
//	Updates the entire deployment configuration.
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
//	    name: deployment
//	    description: Deployment configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentPut"
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
func deploymentPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Deployment.
	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = util.EtagCheck(r, deployment.Info().Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.DeploymentPut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range deployment.Info().Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	err = deployment.Update(&req)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.DeploymentUpdated.Event(projectName, deploymentName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/deployments/{name} deployments deployments_post
//
//	Rename the deployment
//
//	Renames an existing deployment.
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
//	    name: Deployment
//	    description: Deployment rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.DeploymentPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the existing Deployment.
	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	err = deployment.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	lc := lifecycle.DeploymentRenamed.Event(projectName, deploymentName, request.CreateRequestor(r), logger.Ctx{"old_name": deploymentName})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// API endpoints

// swagger:operation GET /1.0/deployments/{deploymentName}/instance-sets/{instSetName} deployments deployment_instance_sets_get
//
//  Get the deployment instance sets
//
//  Returns a list of deployment instance sets (URLs).
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
//                "/1.0/deployments/foo/instance-sets/web",
//                "/1.0/deployments/foo/instance-sets/bar",
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/deployments/{deploymentName}/instance-sets/{instSetName}?recursion=1 deployments deployments_instance_sets_get_recursion1
//
//	Get the deployment instance sets
//
//	Returns a list of deployment instance sets (structs).
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
//	          description: List of deployment instance sets
//	          items:
//	            $ref: "#/definitions/DeploymentInstanceSet"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstanceSetsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeployment *db.Deployment
	var dbInstanceSets []*db.DeploymentInstanceSet
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err = tx.GetDeployment(ctx, projectName, deploymentName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment: %w", err)
		}

		dbInstanceSets, err = tx.GetDeploymentInstanceSets(ctx, dbDeployment.ID)
		if err != nil {
			return fmt.Errorf("Failed loading deployment instance sets: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if util.IsRecursionRequest(r) {
		instSets := make([]*api.DeploymentInstanceSet, 0, len(dbInstanceSets))
		for _, dbInstanceSet := range dbInstanceSets {
			instSets = append(instSets, &dbInstanceSet.DeploymentInstanceSet)
		}

		return response.SyncResponse(true, instSets)
	}

	instSetURLs := make([]string, 0, len(dbInstanceSets))
	for _, dbInstanceSet := range dbInstanceSets {
		instSetURLs = append(instSetURLs, dbInstanceSet.DeploymentInstanceSet.URL(version.APIVersion, dbDeployment.Name, dbInstanceSet.Name).String())
	}

	return response.SyncResponse(true, instSetURLs)
}

// swagger:operation POST /1.0/deployments/{deploymentName}/instance-sets/{instSetName} deployments deployments_instance_set_post
//
//	Add a deployment instance set
//
//	Creates a new deployment instance set.
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
//	    name: instance set
//	    description: instance set
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentInstanceSetsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstanceSetsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.DeploymentInstanceSetsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployment.InstanceSetCreate(req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating deployment instance set: %w", err))
	}

	lc := lifecycle.DeploymentInstanceSetCreated.Event(projectName, deploymentName, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse // tomp TODO use operation here.
}

// swagger:operation DELETE /1.0/deployments/{deploymentName}/instance-sets/{instSetName} deployments deployment_instance_set_delete
//
//	Delete the deployment instance set
//
//	Removes the deployment instance set.
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
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstanceSetDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	instSetName, err := url.PathUnescape(mux.Vars(r)["instanceSet"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	err = deployment.InstanceSetDelete(instSetName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting instance set: %w", err))
	}

	lc := lifecycle.DeploymentInstanceSetDeleted.Event(projectName, deploymentName, instSetName, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/deployments/{deploymentName}/instance-sets/{instSetName} deployments deployment_instance_set_get
//
//	Get the deployment instance set
//
//	Gets a specific deployment instance set.
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
//	    description: Deployment instance set
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
//	          $ref: "#/definitions/DeploymentInstanceSet"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstanceSetGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	instSetName, err := url.PathUnescape(mux.Vars(r)["instanceSet"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeployment *db.Deployment
	var dbInstanceSet *db.DeploymentInstanceSet
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err = tx.GetDeployment(ctx, projectName, deploymentName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment: %w", err)
		}

		dbInstanceSet, err = tx.GetDeploymentInstanceSet(ctx, dbDeployment.ID, instSetName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment instance set: %w", err)
		}

		// tomp TODO add UsedBy

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, dbInstanceSet.DeploymentInstanceSet, dbInstanceSet.DeploymentInstanceSet.Etag())
}

// swagger:operation PUT /1.0/deployments/{deploymentName}/instance-sets/{instSetName} deployments deployment_instance_set_put
//
//	Update the deployment instance set
//
//	Updates the deployment instance set.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: Deployment instance set
//	    description: Deployment instance set configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentInstanceSetPut"
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
func deploymentInstanceSetPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	instSetName, err := url.PathUnescape(mux.Vars(r)["instanceSet"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.DeploymentInstanceSetPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployment.InstanceSetUpdate(instSetName, req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating deployment instance set: %w", err))
	}

	lc := lifecycle.DeploymentInstanceSetUpdated.Event(projectName, deploymentName, instSetName, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/deployments/{deploymentName}/instance-sets/{instSetName} deployments deployments_instance_set_post
//
//	Rename the deployment instance set
//
//	Renames an existing deployment instance set.
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
//	    name: Deployment
//	    description: Deployment instance set rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentInstanceSetPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstanceSetPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName := projectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	instSetName, err := url.PathUnescape(mux.Vars(r)["instanceSet"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName)
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.DeploymentInstanceSetPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployment.InstanceSetUpdate(instSetName, req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed renaming deployment instance set: %w", err))
	}

	lc := lifecycle.DeploymentInstanceSetUpdated.Event(projectName, deploymentName, instSetName, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}
