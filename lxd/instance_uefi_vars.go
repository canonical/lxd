package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
)

// swagger:operation GET /1.0/instances/{name}/uefi-vars instances instance_uefi_vars_get
//
//	Get the instance's UEFI variables
//
//	Gets the UEFI variables for a specific VM.
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
//	    description: Instance UEFI variables
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
//	          $ref: "#/definitions/InstanceUEFIVars"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceUEFIVarsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, _, _, resp := forwardedInstanceResponseWithInstance(s, r)
	if resp != nil {
		return resp
	}

	if inst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("UEFI variables manipulation supported for VM type instances only"))
	}

	instanceUEFI, err := inst.(instance.VM).UEFIVars()
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{instanceUEFI}

	return response.SyncResponseETag(true, instanceUEFI, etag)
}

// swagger:operation PUT /1.0/instances/{name}/uefi-vars instances instance_uefi_vars_put
//
//	Set the instance's UEFI variables
//
//	Sets the UEFI variables for a specific VM.
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
//	    name: instanceEFI
//	    description: UEFI variables update request
//	    schema:
//	      $ref: "#/definitions/InstanceUEFIVars"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceUEFIVarsPut(d *Daemon, r *http.Request) response.Response {
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

	s := d.State()

	projectName, name, resp := forwardedInstanceResponse(s, r)
	if resp != nil {
		return resp
	}

	unlock, err := instanceOperationLock(s.ShutdownCtx, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	defer unlock()

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("UEFI variables manipulation supported for VM type instances only"))
	}

	if inst.IsRunning() {
		return response.BadRequest(errors.New("UEFI variables editing is allowed for stopped VM instances only"))
	}

	instanceUEFI, err := inst.(instance.VM).UEFIVars()
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{instanceUEFI}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	configRaw := api.InstanceUEFIVars{}
	err = json.NewDecoder(r.Body).Decode(&configRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	err = inst.(instance.VM).UEFIVarsUpdate(configRaw)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
