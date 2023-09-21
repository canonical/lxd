package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/deployments"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var deploymentsCmd = APIEndpoint{
	Path: "deployments",

	Get:  APIEndpointAction{Handler: deploymentsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: deploymentsPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateDeployments)},
}

var deploymentCmd = APIEndpoint{
	Path: "deployments/{deploymentName}",

	Delete: APIEndpointAction{Handler: deploymentDelete, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanEdit, "deploymentName")},
	Get:    APIEndpointAction{Handler: deploymentGet, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanView, "deploymentName")},
	Put:    APIEndpointAction{Handler: deploymentPut, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanEdit, "deploymentName")},
	Patch:  APIEndpointAction{Handler: deploymentPut, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanEdit, "deploymentName")},
	Post:   APIEndpointAction{Handler: deploymentPost, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanEdit, "deploymentName")},
}

var deploymentKeysCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/keys",

	Get:  APIEndpointAction{Handler: deploymentKeysGet, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanAccessDeploymentKeys, "deploymentName")},
	Post: APIEndpointAction{Handler: deploymentKeysPost, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanCreateDeploymentKeys, "deploymentName")},
}

var deploymentKeyCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/keys/{deploymentKeyName}",

	Delete: APIEndpointAction{Handler: deploymentKeyDelete, AccessHandler: allowPermission(auth.ObjectTypeDeploymentKey, auth.EntitlementCanEdit, "deploymentName", "deploymentKeyName")},
	Get:    APIEndpointAction{Handler: deploymentKeyGet, AccessHandler: allowPermission(auth.ObjectTypeDeploymentKey, auth.EntitlementCanView, "deploymentName", "deploymentKeyName")},
	Put:    APIEndpointAction{Handler: deploymentKeyPut, AccessHandler: allowPermission(auth.ObjectTypeDeploymentKey, auth.EntitlementCanEdit, "deploymentName", "deploymentKeyName")},
	Post:   APIEndpointAction{Handler: deploymentKeyPost, AccessHandler: allowPermission(auth.ObjectTypeDeploymentKey, auth.EntitlementCanEdit, "deploymentName", "deploymentKeyName")},
}

var deploymentShapesCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/shapes",

	Get:  APIEndpointAction{Handler: deploymentShapesGet, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanAccessDeploymentShapes, "deploymentName")},
	Post: APIEndpointAction{Handler: deploymentShapesPost, AccessHandler: allowPermission(auth.ObjectTypeDeployment, auth.EntitlementCanCreateDeploymentShapes, "deploymentName")},
}

var deploymentShapeCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/shapes/{deploymentShapeName}",

	Delete: APIEndpointAction{Handler: deploymentShapeDelete, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShape, auth.EntitlementCanEdit, "deploymentName", "deploymentShapeName")},
	Get:    APIEndpointAction{Handler: deploymentShapeGet, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShape, auth.EntitlementCanView, "deploymentName", "deploymentShapeName")},
	Put:    APIEndpointAction{Handler: deploymentShapePut, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShape, auth.EntitlementCanEdit, "deploymentName", "deploymentShapeName")},
	Post:   APIEndpointAction{Handler: deploymentShapePost, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShape, auth.EntitlementCanEdit, "deploymentName", "deploymentShapeName")},
}

var deploymentShapeInstancesCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/shapes/{deploymentShapeName}/instances",

	Get:  APIEndpointAction{Handler: deploymentInstancesGet, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShape, auth.EntitlementCanAccessDeployedInstances, "deploymentName", "deploymentShapeName")},
	Post: APIEndpointAction{Handler: deploymentInstancesPost, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShape, auth.EntitlementCanDeployInstances, "deploymentName", "deploymentShapeName")},
}

var deployedShapeInstancesCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/shapes/{deploymentShapeName}/instances/{name}",

	Delete: APIEndpointAction{Handler: deploymentInstanceDelete, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShapeInstance, auth.EntitlementCanEdit, "deploymentName", "deploymentShapeName", "name")},
}

var deployedShapeInstancesStateCmd = APIEndpoint{
	Path: "deployments/{deploymentName}/shapes/{deploymentShapeName}/instances/{name}/state",

	Put: APIEndpointAction{Handler: deploymentInstanceState, AccessHandler: allowPermission(auth.ObjectTypeDeploymentShapeInstance, auth.EntitlementCanEdit, "deploymentName", "deploymentShapeName", "name")},
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

	projectName := request.ProjectParam(r)

	var err error

	deployments, err := deployments.LoadByProject(s, projectName, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Sort by deployments name.
	sort.SliceStable(deployments, func(i, j int) bool {
		deploymentA := deployments[i]
		deploymentB := deployments[j]

		return deploymentA.Info().Name < deploymentB.Info().Name
	})

	if util.IsRecursionRequest(r) {
		apiDeployments := make([]*api.Deployment, 0, len(deployments))
		for _, dep := range deployments {
			apiDep := dep.Info()
			usedBy, err := dep.UsedBy()
			if err != nil {
				return response.SmartError(err)
			}

			apiDep.UsedBy = usedBy
			apiDeployments = append(apiDeployments, apiDep)
		}

		return response.SyncResponse(true, apiDeployments)
	}

	urls := make([]string, 0, len(deployments))
	for _, dep := range deployments {
		urls = append(urls, dep.Info().URL(version.APIVersion, projectName).String())
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

	projectName := request.ProjectParam(r)

	req := api.DeploymentsPost{}

	// Parse the request into a record.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	_, err = deployments.LoadByName(s, projectName, req.Name, false)
	if err == nil {
		return response.BadRequest(fmt.Errorf("The deployment already exists"))
	}

	err = deployments.Create(s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.AddDeployment(s.ShutdownCtx, projectName, req.Name)
	if err != nil {
		logger.Error("Failed to add deployment to authorizer", logger.Ctx{"name": req.Name, "project": projectName, "error": err})
	}

	lc := lifecycle.DeploymentCreated.Event(projectName, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/deployments/{deploymentName} deployments deployments_delete
//
//	Delete the deployment
//
//	Removes the deployment.
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

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	err = deployment.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteDeployment(s.ShutdownCtx, projectName, deploymentName)
	if err != nil {
		logger.Error("Failed to remove deployment from authorizer", logger.Ctx{"name": deploymentName, "project": projectName, "error": err})
	}

	s.Events.SendLifecycle(projectName, lifecycle.DeploymentDeleted.Event(projectName, deploymentName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/deployments/{deploymentName} deployments deployments_get
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

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	apiDep := deployment.Info()
	usedBy, err := deployment.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	apiDep.UsedBy = usedBy

	return response.SyncResponseETag(true, apiDep, apiDep.Etag())
}

// swagger:operation PATCH /1.0/deployments/{deploymentName} deployments deployments_patch
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

// swagger:operation PUT /1.0/deployments/{deploymentName} deployments deployments_put
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

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Deployment.
	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
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

// swagger:operation POST /1.0/deployments/{deploymentName} deployments deployments_post
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

	projectName := request.ProjectParam(r)

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
	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	err = deployment.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.RenameDeployment(s.ShutdownCtx, projectName, deploymentName, req.Name)
	if err != nil {
		logger.Error("Failed to rename deployment in authorizer", logger.Ctx{"oldName": deploymentName, "newName": req.Name, "project": projectName, "error": err})
	}

	lc := lifecycle.DeploymentRenamed.Event(projectName, deploymentName, request.CreateRequestor(r), logger.Ctx{"old_name": deploymentName})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// API endpoints.

// swagger:operation GET /1.0/deployments/{deploymentName}/keys deployments deployment_keys_get
//
//  Get the deployment keys for a particular deployment
//
//  Returns a list of deployment keys (URLs) for a deployment.
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
//                "/1.0/deployments/dep1/keys/key1",
//                "/1.0/deployments/dep1/keys/key2"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/deployments/{deploymentName}/keys?recursion=1 deployments deployment_keys_get_recursion1
//
//	Get the deployment keys for a particular deployment
//
//	Returns a list of deployment keys (structs) for a particular deployment.
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
//	    description: Deployment Keys
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
//	          description: List of deployment keys for a deployment
//	          items:
//	            $ref: "#/definitions/DeploymentKey"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentKeysGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeploymentKeys []*db.DeploymentKey

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filters := []db.DeploymentKeyFilter{{
			ProjectName:    &projectName,
			DeploymentName: &deploymentName,
		}}

		dbDeploymentKeys, err = tx.GetDeploymentKeys(ctx, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading deployments: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Sort by deployment keys name.
	sort.SliceStable(dbDeploymentKeys, func(i, j int) bool {
		deploymentKeyA := dbDeploymentKeys[i]
		deploymentKeyB := dbDeploymentKeys[j]

		return deploymentKeyA.Name < deploymentKeyB.Name
	})

	if util.IsRecursionRequest(r) {
		depKeys := make([]*api.DeploymentKey, 0, len(dbDeploymentKeys))
		for _, dbDeploymentKey := range dbDeploymentKeys {
			depKeys = append(depKeys, &dbDeploymentKey.DeploymentKey)
		}

		return response.SyncResponse(true, depKeys)
	}

	urls := make([]string, 0, len(dbDeploymentKeys))
	for _, dbDeploymentKey := range dbDeploymentKeys {
		urls = append(urls, dbDeploymentKey.DeploymentKey.URL(version.APIVersion, projectName, deploymentName).String())
	}

	return response.SyncResponse(true, urls)
}

// swagger:operation POST /1.0/deployments/{deploymentName}/keys deployments deployment_keys_post
//
//	Add a deployment key
//
//	Creates a new deployment key.
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
//	    name: deployment_key
//	    description: Deployment Key
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentKeysPost"
//	responses:
//	  "200":
//	    $ref: "#/definitions/EmptySyncResponse"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentKeysPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.DeploymentKeysPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployments.CreateDeploymentKey(s, projectName, deploymentName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.AddDeploymentKey(s.ShutdownCtx, projectName, deploymentName, req.Name)
	if err != nil {
		logger.Error("Failed to add deployment key to authorizer", logger.Ctx{"deploymentKeyName": req.Name, "deploymentName": deploymentName, "project": projectName, "error": err})
	}

	return response.EmptySyncResponse
}

// API endpoints.

// swagger:operation DELETE /1.0/deployments/{deploymentName}/keys/{deploymentKeyName} deployments deployment_key_delete
//
//	Delete the deployment key
//
//	Removes the deployment key.
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
func deploymentKeyDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentKeyName, err := url.PathUnescape(mux.Vars(r)["deploymentKeyName"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err = tx.DeleteDeploymentKey(ctx, projectName, deploymentName, deploymentKeyName)
		if err != nil {
			return fmt.Errorf("Failed deleting deployment key: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteDeploymentKey(s.ShutdownCtx, projectName, deploymentName, deploymentKeyName)
	if err != nil {
		logger.Error("Failed to remove deployment key from authorizer", logger.Ctx{"deploymentKeyName": deploymentKeyName, "deploymentName": deploymentName, "project": projectName, "error": err})
	}

	s.Events.SendLifecycle(projectName, lifecycle.DeploymentKeyDeleted.Event(projectName, deploymentName, deploymentKeyName, request.CreateRequestor(r), nil))
	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/deployments/{deploymentName}/keys/{deploymentKeyName} deployments deployment_key_get
//
//	Get the deployment key
//
//	Gets a specific deployment key.
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
//	    description: Deployment Key
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
//	          $ref: "#/definitions/DeploymentKey"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentKeyGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentKeyName, err := url.PathUnescape(mux.Vars(r)["deploymentKeyName"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeploymentKey *db.DeploymentKey
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := db.DeploymentKeyFilter{
			ProjectName:       &projectName,
			DeploymentName:    &deploymentName,
			DeploymentKeyName: &deploymentKeyName,
		}

		dbDeploymentKeyList, err := tx.GetDeploymentKeys(ctx, filter)
		if err != nil {
			return fmt.Errorf("Failed loading deployment key: %w", err)
		}

		if len(dbDeploymentKeyList) != 1 {
			return fmt.Errorf("Deployment key not found")
		}

		dbDeploymentKey = dbDeploymentKeyList[0]
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, dbDeploymentKey.DeploymentKey, dbDeploymentKey.DeploymentKey.Etag())
}

// swagger:operation PUT /1.0/deployments/{deploymentName}/keys/{deploymentKeyName} deployments deployment_key_put
//
//	Update the deployment key
//
//	Updates the deployment key configuration.
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
//	    name: deployment_key
//	    description: Deployment key configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentKeyPut"
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
func deploymentKeyPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentKeyName, err := url.PathUnescape(mux.Vars(r)["deploymentKeyName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentWithKey, err := deployments.LoadByName(s, projectName, deploymentName, true)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = util.EtagCheck(r, deploymentWithKey.InfoDeploymentKey().Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.DeploymentKeyPut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.UpdateDeploymentKey(ctx, projectName, deploymentName, deploymentKeyName, &req)
		if err != nil {
			return fmt.Errorf("Failed updating deployment key: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.DeploymentKeyUpdated.Event(projectName, deploymentName, deploymentKeyName, request.CreateRequestor(r), nil))
	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/deployments/{deploymentName}/keys/{deploymentKeyName} deployments deployment_key_post
//
//	Rename the deployment key
//
//	Renames an existing deployment key.
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
//	    name: deployment_key
//	    description: Deployment Key rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentKeyPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentKeyPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentKeyName, err := url.PathUnescape(mux.Vars(r)["deploymentKeyName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.DeploymentKeyPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	deploymentWithKey, err := deployments.LoadByName(s, projectName, deploymentName, true)
	if err != nil {
		return response.SmartError(err)
	}

	// check that the name matches the name in the url
	if deploymentKeyName != deploymentWithKey.InfoDeploymentKey().Name {
		return response.BadRequest(fmt.Errorf("Deployment key name doesn't match URL"))
	}

	err = deploymentWithKey.RenameDeploymentKey(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.RenameDeploymentKey(s.ShutdownCtx, projectName, deploymentName, deploymentKeyName, req.Name)
	if err != nil {
		logger.Error("Failed to rename deployment key in authorizer", logger.Ctx{"oldDeploymentKeyName": deploymentKeyName, "newDeploymentKeyName": req.Name, "deploymentName": deploymentName, "project": projectName, "error": err})
	}

	lc := lifecycle.DeploymentKeyRenamed.Event(projectName, deploymentName, deploymentKeyName, request.CreateRequestor(r), logger.Ctx{"old_name": deploymentKeyName})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// API endpoints.

// swagger:operation GET /1.0/deployments/{deploymentName}/shapes deployments deployment_shapes_get
//
//  Get the deployment shapes
//
//  Returns a list of deployment shapes (URLs).
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
//                "/1.0/deployments/foo/shapes/web",
//                "/1.0/deployments/foo/shapes/bar",
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/deployments/{deploymentName}/shapes?recursion=1 deployments deployment_shapes_get_recursion1
//
//	Get the deployment deploymentShapes
//
//	Returns a list of deployment deploymentShapes (structs).
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
//	          description: List of deployment shapes
//	          items:
//	            $ref: "#/definitions/DeploymentShape"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentShapesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeployment *db.Deployment
	var dbDeploymentShapes []*db.DeploymentShape
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err = tx.GetDeployment(ctx, projectName, deploymentName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment: %w", err)
		}

		dbDeploymentShapes, err = tx.GetDeploymentShapes(ctx, dbDeployment.ID)
		if err != nil {
			return fmt.Errorf("Failed loading deployment shapes: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if util.IsRecursionRequest(r) {
		deploymentShapes := make([]*api.DeploymentShape, 0, len(dbDeploymentShapes))
		for _, dbDeploymentShape := range dbDeploymentShapes {
			deploymentShapes = append(deploymentShapes, &dbDeploymentShape.DeploymentShape)
		}

		return response.SyncResponse(true, deploymentShapes)
	}

	deploymentShapeURLs := make([]string, 0, len(dbDeploymentShapes))
	for _, dbDeploymentShape := range dbDeploymentShapes {
		deploymentShapeURLs = append(deploymentShapeURLs, dbDeploymentShape.DeploymentShape.URL(version.APIVersion, dbDeployment.Name, dbDeploymentShape.Name).String())
	}

	return response.SyncResponse(true, deploymentShapeURLs)
}

func validateShapeScale(minimum int, maximum int) error {
	if minimum < 0 || maximum < 0 {
		return fmt.Errorf("Scaling values must be greater than or equal to 0")
	}

	if minimum == 0 && maximum == 0 {
		return fmt.Errorf("Scaling values cannot both be 0")
	}

	if minimum > maximum {
		return fmt.Errorf("Scaling minimum cannot be greater than scaling maximum")
	}

	return nil
}

// swagger:operation POST /1.0/deployments/{deploymentName}/shapes deployments deployment_shapes_post
//
//	Add a deployment shape
//
//	Creates a new deployment shape.
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
//	    name: deployment_shape
//	    description: deployment shape
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
func deploymentShapesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.DeploymentShapesPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// validate scaling parameters
	err = validateShapeScale(req.ScalingMinimum, req.ScalingMaximum)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployment.DeploymentShapeCreate(req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating deployment shape: %w", err))
	}

	err = s.Authorizer.AddDeploymentShape(s.ShutdownCtx, projectName, deploymentName, req.Name)
	if err != nil {
		logger.Error("Failed to add deployment shape to authorizer", logger.Ctx{"deploymentShapeName": req.Name, "deploymentName": deploymentName, "project": projectName, "error": err})
	}

	lc := lifecycle.DeploymentShapeCreated.Event(projectName, deploymentName, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName} deployments deployment_shape_delete
//
//	Delete the deployment shape
//
//	Removes the deployment shape.
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
func deploymentShapeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentShapeName, err := url.PathUnescape(mux.Vars(r)["deploymentShapeName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	err = deployment.DeploymentShapeDelete(deploymentShapeName)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteDeploymentShape(s.ShutdownCtx, projectName, deploymentName, deploymentShapeName)
	if err != nil {
		logger.Error("Failed to remove deployment shape from authorizer", logger.Ctx{"deploymentShapeName": deploymentShapeName, "deploymentName": deploymentName, "project": projectName, "error": err})
	}

	lc := lifecycle.DeploymentShapeDeleted.Event(projectName, deploymentName, deploymentShapeName, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName} deployments deployment_shape_get
//
//	Get the deployment shape
//
//	Gets a specific deployment shape.
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
//	    description: DeploymentShape
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
func deploymentShapeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentShapeName, err := url.PathUnescape(mux.Vars(r)["deploymentShapeName"])
	if err != nil {
		return response.SmartError(err)
	}

	var dbDeployment *db.Deployment
	var dbDeploymentShape *db.DeploymentShape
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err = tx.GetDeployment(ctx, projectName, deploymentName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment: %w", err)
		}

		dbDeploymentShape, err = tx.GetDeploymentShape(ctx, dbDeployment.ID, deploymentShapeName)
		if err != nil {
			return fmt.Errorf("Failed loading deployment shape: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, dbDeploymentShape.DeploymentShape, dbDeploymentShape.DeploymentShape.Etag())
}

// swagger:operation PUT /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName} deployments deployment_shape_put
//
//	Update the deployment shape
//
//	Updates the deployment shape.
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
//	    name: DeploymentShape
//	    description: DeploymentShape configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentShapePut"
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
func deploymentShapePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentShapeName, err := url.PathUnescape(mux.Vars(r)["deploymentShapeName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.DeploymentShapePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// validate scaling parameters
	err = validateShapeScale(req.ScalingMinimum, req.ScalingMaximum)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployment.DeploymentShapeUpdate(deploymentShapeName, req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating deployment shape: %w", err))
	}

	lc := lifecycle.DeploymentShapeUpdated.Event(projectName, deploymentName, deploymentShapeName, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName} deployments deployment_shape_post
//
//	Rename the deployment shape
//
//	Renames an existing deployment shape.
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
//	    name: DeploymentShape
//	    description: DeploymentShape rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentShapePost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentShapePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentShapeName, err := url.PathUnescape(mux.Vars(r)["deploymentShapeName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.DeploymentShapePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = deployment.DeploymentShapeRename(deploymentShapeName, req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed renaming deployment shape: %w", err))
	}

	err = s.Authorizer.RenameDeploymentShape(s.ShutdownCtx, projectName, deploymentName, deploymentShapeName, req.Name)
	if err != nil {
		logger.Error("Failed to rename deployment shape in authorizer", logger.Ctx{"oldDeploymentShapeName": deploymentShapeName, "newDeploymentShapeName": req.Name, "deploymentName": deploymentName, "project": projectName, "error": err})
	}

	lc := lifecycle.DeploymentShapeUpdated.Event(projectName, deploymentName, deploymentShapeName, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.EmptySyncResponse
}

// API endpoints.

// swagger:operation GET /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName}/instances deployments deployments_instances_get
//
//  Get the deployed instances within the shape of a deployment
//
//  Returns a list of instances (URLs) deployed within the shape of a deployment.
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
//                "/1.0/instances/c1",
//                "/1.0/instances/c2",
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName}/instances?recursion=1 deployments deployments_instances_get_recursion1
//
//	Get the deployed instances within the shape of a deployment
//
//	Returns a list of instances (structs) deployed within the shape of a deployment.
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
//	          description: List of deployed instances in an shape of a deployment
//	          items:
//	            $ref: "#/definitions/Instance"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstancesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	var err error

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentShapeName, err := url.PathUnescape(mux.Vars(r)["deploymentShapeName"])
	if err != nil {
		return response.SmartError(err)
	}

	deployedInstances := make([]*api.Instance, 0)
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filters := []db.DeploymentInstanceFilter{{
			DeploymentName:      &deploymentName,
			DeploymentShapeName: &deploymentShapeName,
		}}

		instances, err := dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Project: &projectName})
		if err != nil {
			return err
		}

		deploymentShapeInstanceIDs, err := tx.GetDeploymentShapeInstanceIDs(ctx, filters...)
		if err != nil {
			return err
		}

		for _, instance := range instances {
			if shared.ValueInSlice(int64(instance.ID), deploymentShapeInstanceIDs) {
				apiInstance, err := instance.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				deployedInstances = append(deployedInstances, apiInstance)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Sort by deployments name.
	sort.SliceStable(deployedInstances, func(i, j int) bool {
		deployedInstanceA := deployedInstances[i]
		deployedInstanceB := deployedInstances[j]

		return deployedInstanceA.Name < deployedInstanceB.Name
	})

	if util.IsRecursionRequest(r) {
		return response.SyncResponse(true, deployedInstances)
	}

	urls := make([]string, 0, len(deployedInstances))
	for _, instance := range deployedInstances {
		urls = append(urls, instance.URL(version.APIVersion, projectName).String())
	}

	return response.SyncResponse(true, urls)
}

// swagger:operation POST /1.0/deployments/{deploymentName}/instances deployments deployments_instance_post
//
//	Add a new instance in an deployment shape
//
//	Creates a new instance within an deployment shape that will match the instance template and the scale of the deployment shape.
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
//	    name: DeploymentInstancesPost
//	    description: Deployed instance creation request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/DeploymentInstancesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstancesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.DeploymentInstancesPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the deployment.
	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the deployment shape.
	deploymentShape, err := deployment.DeploymentShapeGet(req.ShapeName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check scaling constraints.
	if deploymentShape.ScalingMaximum <= deploymentShape.ScalingCurrent {
		return response.BadRequest(fmt.Errorf("The deployment shape %s is already at its maximum scale", req.ShapeName))
	}

	newReq := r // Forge a new request with the instance template.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		instances, err := dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Project: &projectName})
		if err != nil {
			return err
		}

		for _, instance := range instances {
			if instance.Name == req.InstanceName {
				return fmt.Errorf("Instance %q already exists in the deployment shape %s", req.InstanceName, req.ShapeName)
			}
		}

		// Get the instance template and insert the name of the instance we want to create.
		instanceTemplate := deploymentShape.InstanceTemplate
		if shared.IsZero(instanceTemplate) {
			return fmt.Errorf("The deployment shape %s doesn't have an instance template", req.ShapeName)
		}

		if shared.IsZero(instanceTemplate.Source) {
			return fmt.Errorf("The deployment shape %s doesn't have an instance template source", req.ShapeName)
		}

		instanceTemplate.Source.Type = "image"
		instanceTemplate.Name = req.InstanceName
		data, err := json.Marshal(instanceTemplate)
		if err != nil {
			return fmt.Errorf("Failed marshalling instance template: %w", err)
		}

		newReq.Body = ioutil.NopCloser(bytes.NewReader(data))
		newReq.ContentLength = int64(len(data))
		newReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
		newReq.Header.Set("Content-Type", "application/json")

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return deploymentInstanceCreate(d, newReq, deploymentName, req.ShapeName)
}

// swagger:operation DELETE /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName}/instances/{name} deployments deployments_instance_delete
//
//	Delete the instance from the deployment shape
//
//	Removes the instance from the deployment shape.
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
func deploymentInstanceDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	deploymentName, err := url.PathUnescape(mux.Vars(r)["deploymentName"])
	if err != nil {
		return response.SmartError(err)
	}

	deploymentShapeName, err := url.PathUnescape(mux.Vars(r)["deploymentShapeName"])
	if err != nil {
		return response.SmartError(err)
	}

	instanceName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the deployment.
	deployment, err := deployments.LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the deployment shape.
	deploymentShape, err := deployment.DeploymentShapeGet(deploymentShapeName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check scaling constraints.
	if deploymentShape.ScalingMinimum >= deploymentShape.ScalingCurrent {
		return response.BadRequest(fmt.Errorf("The deployment shape %s is already at its minimum scale", deploymentShapeName))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filters := []db.DeploymentInstanceFilter{{
			DeploymentName:      &deploymentName,
			DeploymentShapeName: &deploymentShapeName,
		}}

		instances, err := dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Project: &projectName})
		if err != nil {
			return err
		}

		// Before we do any instance creation logic, check if the instance already exists within this deployment.
		deploymentShapeInstanceIDs, err := tx.GetDeploymentShapeInstanceIDs(ctx, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading deployed instances: %w", err)
		}

		instanceFound := false
		for _, instance := range instances {
			if shared.ValueInSlice(int64(instance.ID), deploymentShapeInstanceIDs) {
				if instance.Name == instanceName {
					instanceFound = true
					break
				}
			}
		}

		if !instanceFound {
			return fmt.Errorf("Instance %s does not exist in the deployment shape %s", instanceName, deploymentShapeName)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Stop the instance forcefully.
	stopReq := api.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   true,
	}

	stopData, err := json.Marshal(stopReq)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed marshalling instance stop state request: %w", err))
	}

	newReqStop := r
	for key, values := range r.Header {
		for _, value := range values {
			newReqStop.Header.Add(key, value)
		}
	}

	newReqStop.Body = ioutil.NopCloser(bytes.NewReader(stopData))
	newReqStop.ContentLength = int64(len(stopData))
	newReqStop.Header.Set("Content-Length", fmt.Sprintf("%d", len(stopData)))
	newReqStop.Header.Set("Content-Type", "application/json")

	newReqStop = newReqStop.WithContext(r.Context())
	op, err := deploymentInstanceStop(d, newReqStop)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed stopping instance: %w", err))
	}

	err = op.Start()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed starting instance stop operation: %w", err))
	}

	err = op.Wait(context.Background())
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed waiting for instance to stop: %w", err))
	}

	// Delete deployed instance.
	newReqDelete := r
	for key, values := range r.Header {
		for _, value := range values {
			newReqDelete.Header.Add(key, value)
		}
	}

	newReqDelete = newReqDelete.WithContext(r.Context())

	return instanceDeleteCommon(d, newReqDelete, deploymentName, deploymentShapeName)
}

// swagger:operation PUT /1.0/deployments/{deploymentName}/shapes/{deploymentShapeName}/instances/{name}/state deployments deployments_instance_state_put
//
//	Change the state
//
//	Changes the running state of the instance within a deployment.
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
//	    description: State
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceStatePut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func deploymentInstanceState(d *Daemon, r *http.Request) response.Response {
	_, resp, _ := instanceStatePutCommon(d, r)
	return resp
}
