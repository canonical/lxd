package main

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
)

// swagger:operation DELETE /1.0/instances/{name} instances instance_delete
//
// Delete an instance
//
// Deletes a specific instance.
//
// This also deletes anything owned by the instance such as snapshots and backups.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceDelete(d *Daemon, r *http.Request) response.Response {
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Instance is running"))
	}

	rmct := func(op *operations.Operation) error {
		return inst.Delete(false)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, operationtype.InstanceDelete, resources, nil, rmct, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
