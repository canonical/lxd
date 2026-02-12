package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

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
//	  - in: query
//	    name: force
//	    description: Force delete of running instances
//	    type: boolean
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

	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	force := shared.IsTrue(r.FormValue("force"))
	op, err := doInstanceDelete(r.Context(), s, name, projectName, force)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationResponse(op)
}

// doInstanceDelete deletes an instance in the given project.
// If the instance is running and force is true, the instance is force stopped asynchronously
// as part of the delete operation. If the instance is running and force is false, the request
// fails before the operation is created.
func doInstanceDelete(ctx context.Context, s *state.State, name string, projectName string, force bool) (*operations.Operation, error) {
	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize instance delete operation: %w", err)
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return nil, err
	}

	// Pre-check for immediate 400 when force isn't set.
	instRunning := inst.IsRunning()
	if instRunning && !force {
		return nil, api.NewStatusError(http.StatusBadRequest, "Instance is running")
	}

	rmct := func(ctx context.Context, op *operations.Operation) error {
		if instRunning {
			// Stop instance.
			err := doInstanceStatePut(inst, api.InstanceStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			})
			if err != nil {
				return fmt.Errorf("Failed force stopping instance %q before deletion: %w", name, err)
			}

			// Ephemeral instances are automatically deleted when stopped.
			if inst.IsEphemeral() {
				return nil
			}
		}

		return inst.Delete(false, "")
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName),
		Type:        operationtype.InstanceDelete,
		Class:       operations.OperationClassTask,
		RunHook:     rmct,
	}

	op, err := operations.CreateUserOperation(s, requestor, args)
	if err != nil {
		return nil, err
	}

	return op, nil
}
