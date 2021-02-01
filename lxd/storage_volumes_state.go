package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
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

func storagePoolVolumeTypeStateGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume type.
	volumeName := mux.Vars(r)["name"]

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
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Load the storage pool.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Fetch the current usage.
	var used int64
	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Custom volumes.
		used, err = pool.GetCustomVolumeUsage(projectName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		// Instance volumes.
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, volumeName)
		if err != nil {
			return response.NotFound(err)
		}

		used, err = pool.GetInstanceUsage(inst)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Prepare the state struct.
	state := api.StorageVolumeState{}

	// Only fill usage struct if receiving a valid value.
	if used >= 0 {
		state.Usage = &api.StorageVolumeStateUsage{
			Used: uint64(used),
		}
	}

	return response.SyncResponse(true, state)
}
