package main

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

var storagePoolVolumeTypeStateCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/state",

	Get: APIEndpointAction{Handler: storagePoolVolumeTypeStateGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/state storage storage_pool_volume_type_state_get
//
//	Get the storage volume state
//
//	Gets a specific storage volume state (usage data).
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
//	    description: Storage pool
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
//	          $ref: "#/definitions/StorageVolumeState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeStateGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, []int{db.StoragePoolVolumeTypeCustom, db.StoragePoolVolumeTypeContainer, db.StoragePoolVolumeTypeVM}) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the storage project name.
	projectName, err := project.StorageVolumeProject(d.State().DB.Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Load the storage pool.
	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Fetch the current usage.
	var usage *storagePools.VolumeUsage
	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Custom volumes.
		usage, err = pool.GetCustomVolumeUsage(projectName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, volumeName, instancetype.Any)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}

		// Instance volumes.
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}

		usage, err = pool.GetInstanceUsage(inst)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Prepare the state struct.
	state := api.StorageVolumeState{}
	state.Usage = &api.StorageVolumeStateUsage{}

	// Only fill 'used' field if receiving a valid value.
	if usage.Used >= 0 {
		state.Usage.Used = uint64(usage.Used)
	}

	// Only fill 'total' field if receiving a valid value.
	if usage.Total >= 0 {
		state.Usage.Total = usage.Total
	}

	return response.SyncResponse(true, state)
}
