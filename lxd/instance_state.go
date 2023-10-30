package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// swagger:operation GET /1.0/instances/{name}/state instances instance_state_get
//
//	Get the runtime state
//
//	Gets the runtime state of the instance.
//
//	This is a reasonably expensive call as it causes code to be run
//	inside of the instance to retrieve the resource usage and network
//	information.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	responses:
//	  "200":
//	    description: State
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
//	          $ref: "#/definitions/InstanceState"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceState(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	hostInterfaces, _ := net.Interfaces()
	state, err := c.RenderState(hostInterfaces)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, state)
}

// swagger:operation PUT /1.0/instances/{name}/state instances instance_state_put
//
//	Change the state
//
//	Changes the running state of the instance.
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
func instanceStatePut(d *Daemon, r *http.Request) response.Response {
	_, resp, _ := instanceStatePutCommon(d, r)
	return resp
}

func deploymentInstanceStop(d *Daemon, r *http.Request) (*operations.Operation, error) {
	op, _, err := instanceStatePutCommon(d, r)
	if err != nil {
		return nil, err
	}

	return op, nil
}

func instanceStatePutCommon(d *Daemon, r *http.Request) (*operations.Operation, response.Response, error) {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return nil, response.SmartError(err), err
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return nil, response.SmartError(err), err
	}

	if shared.IsSnapshot(name) {
		return nil, response.BadRequest(fmt.Errorf("Invalid instance name")), err
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return nil, response.SmartError(err), err
	}

	if resp != nil {
		return nil, resp, err
	}

	req := api.InstanceStatePut{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant timeout).
	req.Timeout = -1
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return nil, response.BadRequest(err), err
	}

	// Check if the cluster member is evacuated.
	if s.DB.Cluster.LocalNodeIsEvacuated() && req.Action != "stop" {
		return nil, response.Forbidden(fmt.Errorf("Cluster member is evacuated")), err
	}

	// Don't mess with instances while in setup mode.
	<-d.waitReady.Done()

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return nil, response.SmartError(err), err
	}

	// Actually perform the change.
	opType, err := instanceActionToOptype(req.Action)
	if err != nil {
		return nil, response.BadRequest(err), err
	}

	do := func(op *operations.Operation) error {
		inst.SetOperation(op)

		return doInstanceStatePut(inst, req)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, opType, resources, nil, do, nil, nil, r)
	if err != nil {
		return nil, response.InternalError(err), err
	}

	return op, operations.OperationResponse(op), nil
}

func instanceActionToOptype(action string) (operationtype.Type, error) {
	switch shared.InstanceAction(action) {
	case shared.Start:
		return operationtype.InstanceStart, nil
	case shared.Stop:
		return operationtype.InstanceStop, nil
	case shared.Restart:
		return operationtype.InstanceRestart, nil
	case shared.Freeze:
		return operationtype.InstanceFreeze, nil
	case shared.Unfreeze:
		return operationtype.InstanceUnfreeze, nil
	}

	return operationtype.Unknown, fmt.Errorf("Unknown action: '%s'", action)
}

func doInstanceStatePut(inst instance.Instance, req api.InstanceStatePut) error {
	if req.Force {
		// A zero timeout indicates to do a forced stop/restart.
		req.Timeout = 0
	} else if req.Timeout < 0 {
		// If no timeout requested set a high default shutdown timeout. This way if the instance does not
		// respond to shutdown request the operation lock won't linger forever.
		req.Timeout = 600
	}

	timeout := time.Duration(req.Timeout) * time.Second

	switch shared.InstanceAction(req.Action) {
	case shared.Start:
		return inst.Start(req.Stateful)
	case shared.Stop:
		if req.Stateful {
			return inst.Stop(req.Stateful)
		} else if req.Timeout == 0 {
			return inst.Stop(false)
		} else {
			return inst.Shutdown(timeout)
		}

	case shared.Restart:
		return inst.Restart(timeout)
	case shared.Freeze:
		return inst.Freeze()
	case shared.Unfreeze:
		return inst.Unfreeze()
	}

	return fmt.Errorf("Unknown action: '%s'", req.Action)
}
