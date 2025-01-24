package main

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// handleInstanceDelete provides the logic for deleting an instance.
func handleInstanceDelete(s *state.State, projectName, instanceName string) (*operations.Operation, error) {
	if shared.IsSnapshot(instanceName) {
		return nil, fmt.Errorf("Invalid instance name")
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, instanceName)
	if err != nil {
		return nil, fmt.Errorf("Failed to load instance: %w", err)
	}

	if inst.IsRunning() {
		return nil, fmt.Errorf("Instance is running")
	}

	rmct := func(op *operations.Operation) error {
		return inst.Delete(false)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", instanceName)}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceDelete, resources, nil, rmct, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// swagger:operation DELETE /1.0/instances/{name} instances instance_delete
//
//	Delete an instance
//
//	Deletes a specific instance.
//
//	This also deletes anything owned by the instance such as snapshots and backups.
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
func instanceDelete(d *Daemon, r *http.Request) response.Response {
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

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

	// Handle requests targeted to a container on a different node.
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	op, err := handleInstanceDelete(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	op.SetRequestor(r)

	return operations.OperationResponse(op)
}
