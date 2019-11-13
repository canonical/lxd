package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumesCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: AllowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesTypeGet, AccessHandler: AllowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolVolumesTypePost},
}

var storagePoolVolumeTypeContainerCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/container/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeContainerDelete},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeContainerGet, AccessHandler: AllowAuthenticated},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeContainerPatch},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeContainerPost},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeContainerPut},
}

var storagePoolVolumeTypeVMCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/virtual-machine/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeVMDelete},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeVMGet, AccessHandler: AllowAuthenticated},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeVMPatch},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeVMPost},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeVMPut},
}

var storagePoolVolumeTypeCustomCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/custom/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeCustomDelete},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomGet, AccessHandler: AllowAuthenticated},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeCustomPatch},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeCustomPost},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomPut},
}

var storagePoolVolumeTypeImageCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/image/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeImageDelete},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeImageGet, AccessHandler: AllowAuthenticated},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeImagePatch},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeImagePost},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeImagePut},
}

// /1.0/storage-pools/{name}/volumes
// List all storage volumes attached to a given storage pool.
func storagePoolVolumesGet(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	poolName := mux.Vars(r)["name"]

	recursion := util.IsRecursionRequest(r)

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get all volumes currently attached to the storage pool by ID of the
	// pool and project.
	//
	// We exclude volumes of type image, since those are special: they are
	// stored using the storage_volumes table, but are effectively a cache
	// which is not tied to projects, so we always link the to the default
	// project. This means that we want to filter image volumes and return
	// only the ones that have fingerprints matching images actually in use
	// by the project.
	volumes, err := d.cluster.StoragePoolVolumesGet(project, poolID, supportedVolumeTypesExceptImages)
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	imageVolumes, err := d.cluster.StoragePoolVolumesGet("default", poolID, []int{storagePoolVolumeTypeImage})
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	projectImages, err := d.cluster.ImagesGet(project, false)
	if err != nil {
		return response.SmartError(err)
	}
	for _, volume := range imageVolumes {
		if shared.StringInSlice(volume.Name, projectImages) {
			volumes = append(volumes, volume)
		}
	}

	resultString := []string{}
	for _, volume := range volumes {
		apiEndpoint, err := storagePoolVolumeTypeNameToAPIEndpoint(volume.Type)
		if err != nil {
			return response.InternalError(err)
		}

		if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
			apiEndpoint = "container"
		} else if apiEndpoint == storagePoolVolumeAPIEndpointVMs {
			apiEndpoint = "virtual-machine"
		} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
			apiEndpoint = "image"
		}

		if !recursion {
			volName, snapName, ok := shared.ContainerGetParentAndSnapshotName(volume.Name)
			if ok {
				resultString = append(resultString,
					fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s",
						version.APIVersion, poolName, apiEndpoint, volName, snapName))
			} else {
				resultString = append(resultString,
					fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s",
						version.APIVersion, poolName, apiEndpoint, volume.Name))
			}
		} else {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, poolName, volume.Name, volume.Type)
			if err != nil {
				return response.InternalError(err)
			}
			volume.UsedBy = volumeUsedBy
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, volumes)
}

// /1.0/storage-pools/{name}/volumes/{type}
// List all storage volumes of a given volume type for a given storage pool.
func storagePoolVolumesTypeGet(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)

	// Get the name of the pool the storage volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["name"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	recursion := util.IsRecursionRequest(r)

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %s", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volumes of a given volume type currently
	// attached to the storage pool.
	volumes, err := d.cluster.StoragePoolNodeVolumesGetType(volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolume{}
	for _, volume := range volumes {
		if !recursion {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return response.InternalError(err)
			}

			if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
				apiEndpoint = "container"
			} else if apiEndpoint == storagePoolVolumeAPIEndpointVMs {
				apiEndpoint = "virtual-machine"
			} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
				apiEndpoint = "image"
			}

			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, apiEndpoint, volume))
		} else {
			_, vol, err := d.cluster.StoragePoolNodeVolumeGetType(volume, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, poolName, vol.Name, vol.Type)
			if err != nil {
				return response.SmartError(err)
			}
			vol.UsedBy = volumeUsedBy

			resultMap = append(resultMap, vol)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume in a given storage pool.
func storagePoolVolumesTypePost(d *Daemon, r *http.Request) response.Response {
	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	req.Type = mux.Vars(r)["type"]

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if req.Type != storagePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf(`Currently not allowed to create `+
			`storage volumes of type %s`, req.Type))
	}

	poolName := mux.Vars(r)["name"]
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.cluster.StoragePoolNodeVolumeGetTypeByProject("default", req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "migration":
		return doVolumeMigration(d, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}
}

func doVolumeCreateOrCopy(d *Daemon, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	// Check if we can load new storage layer for both target and source pool driver types.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	_, srcPoolErr := storagePools.GetPoolByName(d.State(), req.Source.Pool)
	if err != storageDrivers.ErrUnknownDriver && srcPoolErr != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.SmartError(err)
		}

		run = func(op *operations.Operation) error {
			if req.Source.Name == "" {
				return pool.CreateCustomVolume(req.Name, req.Description, req.Config, op)
			}

			return pool.CreateCustomVolumeFromCopy(req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, req.Source.VolumeOnly, op)
		}
	} else {
		run = func(op *operations.Operation) error {
			return storagePoolVolumeCreateInternal(d.State(), poolName, req)
		}
	}

	// If no source name supplied then this a volume create operation.
	if req.Source.Name == "" {
		err := run(nil)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}

	// Volume copy operations potentially take a long time, so run as an async operation.
	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeCopy, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume of a given volume type in a given storage pool.
func storagePoolVolumesPost(d *Daemon, r *http.Request) response.Response {
	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Check that the user gave use a storage volume type for the storage
	// volume we are about to create.
	if req.Type == "" {
		return response.BadRequest(fmt.Errorf("You must provide a storage volume type of the storage volume"))
	}

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if req.Type != storagePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf(`Currently not allowed to create `+
			`storage volumes of type %s`, req.Type))
	}

	poolName := mux.Vars(r)["name"]
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.cluster.StoragePoolNodeVolumeGetTypeByProject("default", req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "migration":
		return doVolumeMigration(d, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}
}

func doVolumeMigration(d *Daemon, poolName string, req *api.StorageVolumesPost) response.Response {
	// Validate migration mode
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return response.NotImplemented(fmt.Errorf("Mode '%s' not implemented", req.Source.Mode))
	}

	// create new certificate
	var err error
	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			return response.InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return response.InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return response.InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	// Initialise migrationArgs, don't set the Storage property yet, this is done in DoStorage,
	// to avoid this function relying on the legacy storage layer.
	migrationArgs := MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer,
		},
		Secrets:    req.Source.Websockets,
		Push:       push,
		VolumeOnly: req.Source.VolumeOnly,
	}

	sink, err := NewStorageMigrationSink(&migrationArgs)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, req.Name)}

	run := func(op *operations.Operation) error {
		// And finally run the migration.
		err = sink.DoStorage(d.State(), poolName, req, op)
		if err != nil {
			logger.Error("Error during migration sink", log.Ctx{"err": err})
			return fmt.Errorf("Error transferring storage volume: %s", err)
		}

		return nil
	}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(d.State(), "", operations.OperationClassWebsocket, db.OperationVolumeCreate, resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeCopy, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return operations.OperationResponse(op)
}

// /1.0/storage-pools/{name}/volumes/{type}/{name}
// Rename a storage volume of a given volume type in a given storage pool.
// Also supports moving a storage volume between pools and migrating to a different host.
func storagePoolVolumeTypePost(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	req := api.StorageVolumePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	// Check requested new volume name is not a snapshot volume.
	if shared.IsSnapshot(req.Name) {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if volumeTypeName != storagePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Renaming storage volumes of type %s is not allowed", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	var poolID int64
	if req.Pool != "" {
		poolID, err = d.cluster.StoragePoolGetID(req.Pool)
	} else {
		poolID, err = d.cluster.StoragePoolGetID(poolName)
	}
	if err != nil {
		return response.SmartError(err)
	}

	// We need to restore the body of the request since it has already been read, and if we
	// forwarded it now no body would be written out.
	buf := bytes.Buffer{}
	err = json.NewEncoder(&buf).Encode(req)
	if err != nil {
		return response.SmartError(err)
	}
	r.Body = shared.BytesReadCloser{Buf: &buf}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		return storagePoolVolumeTypePostMigration(d.State(), poolName, volumeName, req)
	}

	// Check that the name isn't already in use.
	_, err = d.cluster.StoragePoolNodeVolumeGetTypeID(req.Name, volumeType, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.InternalError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	// Check if the daemon itself is using it.
	used, err := daemonStorageUsed(d.State(), poolName, volumeName)
	if err != nil {
		return response.SmartError(err)
	}

	if used {
		return response.SmartError(fmt.Errorf("Volume is used by LXD itself and cannot be renamed"))
	}

	// Check if a running container is using it.
	ctsUsingVolume, err := storagePoolVolumeUsedByRunningContainersWithProfilesGet(d.State(), poolName, volumeName, volumeTypeName, true)
	if err != nil {
		return response.SmartError(err)
	}

	if len(ctsUsingVolume) > 0 {
		return response.SmartError(fmt.Errorf("Volume is still in use by running containers"))
	}

	// Detect a rename request.
	if req.Pool == "" || req.Pool == poolName {
		return storagePoolVolumeTypePostRename(d, poolName, volumeName, volumeType, req)
	}

	// Otherwise this is a move request.
	return storagePoolVolumeTypePostMove(d, poolName, volumeName, volumeType, req)
}

// storagePoolVolumeTypePostMigration handles volume migration type POST requests.
func storagePoolVolumeTypePostMigration(state *state.State, poolName string, volumeName string, req api.StorageVolumePost) response.Response {
	ws, err := NewStorageMigrationSource(req.VolumeOnly)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, volumeName)}

	run := func(op *operations.Operation) error {
		return ws.DoStorage(state, poolName, volumeName, op)
	}

	if req.Target != nil {
		// Push mode
		err := ws.ConnectStorageTarget(*req.Target)
		if err != nil {
			return response.InternalError(err)
		}

		op, err := operations.OperationCreate(state, "", operations.OperationClassTask, db.OperationVolumeMigrate, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode
	op, err := operations.OperationCreate(state, "", operations.OperationClassWebsocket, db.OperationVolumeMigrate, resources, ws.Metadata(), run, nil, ws.Connect)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// storagePoolVolumeTypePostRename handles volume rename type POST requests.
func storagePoolVolumeTypePostRename(d *Daemon, poolName string, volumeName string, volumeType int, req api.StorageVolumePost) response.Response {
	// Notify users of the volume that it's name is changing.
	err := storagePoolVolumeUpdateUsers(d, poolName, volumeName, req.Pool, req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.SmartError(err)
		}

		err = pool.RenameCustomVolume(volumeName, req.Name, nil)
		if err != nil {
			// Notify users of the volume that it's name is changing back.
			storagePoolVolumeUpdateUsers(d, req.Pool, req.Name, poolName, volumeName)
			return response.SmartError(err)
		}
	} else {
		s, err := storagePoolVolumeInit(d.State(), "default", poolName, volumeName, volumeType)
		if err != nil {
			return response.InternalError(err)
		}

		err = s.StoragePoolVolumeRename(req.Name)
		if err != nil {
			// Notify users of the volume that it's name is changing back.
			storagePoolVolumeUpdateUsers(d, req.Pool, req.Name, poolName, volumeName)
			return response.SmartError(err)
		}
	}

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s", version.APIVersion, poolName, storagePoolVolumeAPIEndpointCustom))
}

// storagePoolVolumeTypePostMove handles volume move type POST requests.
func storagePoolVolumeTypePostMove(d *Daemon, poolName string, volumeName string, volumeType int, req api.StorageVolumePost) response.Response {
	var run func(op *operations.Operation) error

	// Check if we can load new storage layer for both target and source pool driver types.
	srcPool, srcPoolErr := storagePools.GetPoolByName(d.State(), poolName)
	pool, err := storagePools.GetPoolByName(d.State(), req.Pool)
	if err != storageDrivers.ErrUnknownDriver && srcPoolErr != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.SmartError(err)
		}

		run = func(op *operations.Operation) error {
			// Notify users of the volume that it's name is changing.
			err := storagePoolVolumeUpdateUsers(d, poolName, volumeName, req.Pool, req.Name)
			if err != nil {
				return err
			}

			// Provide empty description and nil config to instruct
			// CreateCustomVolumeFromCopy to copy it from source volume.
			err = pool.CreateCustomVolumeFromCopy(req.Name, "", nil, poolName, volumeName, false, op)
			if err != nil {
				// Notify users of the volume that it's name is changing back.
				storagePoolVolumeUpdateUsers(d, req.Pool, req.Name, poolName, volumeName)
				return err
			}

			return srcPool.DeleteCustomVolume(volumeName, op)
		}
	} else {
		// Convert poolName to poolID.
		poolID, _, err := d.cluster.StoragePoolGet(poolName)
		if err != nil {
			return response.SmartError(err)
		}

		// Get the storage volume.
		_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
		if err != nil {
			return response.SmartError(err)
		}

		// Get storage volume snapshots.
		snapshots, err := d.cluster.StoragePoolVolumeSnapshotsGetType(volumeName, volumeType, poolID)
		if err != nil {
			return response.SmartError(err)
		}

		// This is a move request, so copy the volume and then delete the original.
		moveReq := api.StorageVolumesPost{}
		moveReq.Name = req.Name
		moveReq.Type = "custom"
		moveReq.Config = volume.Config
		moveReq.Source.Name = volumeName
		moveReq.Source.Pool = poolName

		run = func(op *operations.Operation) error {
			// Notify users of the volume that it's name is changing.
			err := storagePoolVolumeUpdateUsers(d, poolName, volumeName, req.Pool, req.Name)
			if err != nil {
				return err
			}

			err = storagePoolVolumeCreateInternal(d.State(), req.Pool, &moveReq)
			if err != nil {
				// Notify users of the volume that it's name is changing back.
				storagePoolVolumeUpdateUsers(d, req.Pool, req.Name, poolName, volumeName)
				return err
			}

			// Delete snapshot volumes.
			for _, snapshot := range snapshots {
				s, err := storagePoolVolumeInit(d.State(), "default", poolName, snapshot.Name, volumeType)
				if err != nil {
					return err
				}

				err = s.StoragePoolVolumeSnapshotDelete()
				if err != nil {
					return err
				}
			}

			s, err := storagePoolVolumeInit(d.State(), "default", poolName, volumeName, volumeType)
			if err != nil {
				return err
			}

			err = s.StoragePoolVolumeDelete()
			if err != nil {
				return err
			}

			return nil
		}
	}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeMove, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeTypeContainerPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "container")
}

func storagePoolVolumeTypeVMPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "custom")
}

func storagePoolVolumeTypeImagePost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "image")
}

// storageGetVolumeNameFromURL retrieves the volume name from the URL name segment.
func storageGetVolumeNameFromURL(r *http.Request) (string, error) {
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots.
		return fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2]), nil
	} else if len(fields) > 1 {
		return fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1]), nil
	} else if len(fields) > 0 {
		// Handle volume.
		return fields[0], nil
	}

	return "", fmt.Errorf("Invalid storage volume %s", mux.Vars(r)["name"])
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// Get storage volume of a given volume type on a given storage pool.
func storagePoolVolumeTypeGet(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	project := projectParam(r)

	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %s", volumeTypeName))
	}

	// Get the ID of the storage pool the storage volume is supposed to be
	// attached to.
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the storage volume.
	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, poolName, volume.Name, volume.Type)
	if err != nil {
		return response.SmartError(err)
	}
	volume.UsedBy = volumeUsedBy

	etag := []interface{}{volumeName, volume.Type, volume.Config}

	return response.SyncResponseETag(true, volume, etag)
}

func storagePoolVolumeTypeContainerGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "container")
}

func storagePoolVolumeTypeVMGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "custom")
}

func storagePoolVolumeTypeImageGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// This function does allow limited functionality for non-custom volume types, specifically you
// can modify the volume's description only.
func storagePoolVolumeTypePut(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %s", volumeTypeName))
	}

	poolID, poolRow, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	_, vol, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{volumeName, vol.Type, vol.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.SmartError(err)
		}

		if volumeType == db.StoragePoolVolumeTypeCustom {
			// Restore custom volume from snapshot if requested. This should occur first
			// before applying config changes so that changes are applied to the
			// restored volume.
			if req.Restore != "" {
				err = pool.RestoreCustomVolume(vol.Name, req.Restore, nil)
				if err != nil {
					return response.SmartError(err)
				}
			}

			// Handle update requests.
			err = pool.UpdateCustomVolume(vol.Name, req.Description, req.Config, nil)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			// You are only allowed to modify the description for non-custom volumes.
			// This is a special case because the rootfs devices do not provide a way
			// to update a non-custom volume's description.
			if len(req.Config) > 0 {
				return response.BadRequest(fmt.Errorf("Only description can be modified for volume type %s", volumeTypeName))
			}

			// There is a bug in the lxc client (lxc/storage_volume.go#L829-L865) which
			// means that modifying a snapshot's description gets routed here rather
			// than the dedicated snapshot editing route. So need to handle snapshot
			// volumes here too.

			// Update the database if description changed.
			if req.Description != vol.Description {
				err = d.cluster.StoragePoolVolumeUpdate(vol.Name, volumeType, poolID, req.Description, vol.Config)
				if err != nil {
					response.SmartError(err)
				}
			}
		}
	} else {

		if req.Restore != "" {
			ctsUsingVolume, err := storagePoolVolumeUsedByRunningContainersWithProfilesGet(d.State(), poolName, vol.Name, storagePoolVolumeTypeNameCustom, true)
			if err != nil {
				return response.InternalError(err)
			}

			if len(ctsUsingVolume) != 0 {
				return response.BadRequest(fmt.Errorf("Cannot restore custom volume used by running containers"))
			}

			err = storagePoolVolumeRestore(d.State(), poolName, volumeName, volumeType, req.Restore)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			// Validate the configuration
			err = storagePools.VolumeValidateConfig(volumeName, req.Config, poolRow)
			if err != nil {
				return response.BadRequest(err)
			}

			err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeTypeContainerPut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "container")
}

func storagePoolVolumeTypeVMPut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomPut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "custom")
}

func storagePoolVolumeTypeImagePut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePatch(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %s", volumeTypeName))
	}

	// Get the ID of the storage pool the storage volume is supposed to be attached to.
	poolID, poolRow, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	_, vol, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []interface{}{volumeName, vol.Type, vol.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Merge current config with requested changes.
	for k, v := range vol.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.SmartError(err)
		}

		err = pool.UpdateCustomVolume(vol.Name, req.Description, req.Config, nil)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		// Validate the configuration.
		err = storagePools.VolumeValidateConfig(volumeName, req.Config, poolRow)
		if err != nil {
			return response.BadRequest(err)
		}

		err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeTypeContainerPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "container")
}

func storagePoolVolumeTypeVMPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "custom")
}

func storagePoolVolumeTypeImagePatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypeDelete(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	project := projectParam(r)

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %s", volumeName))
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %s", volumeTypeName))
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	switch volumeType {
	case storagePoolVolumeTypeCustom:
		// allowed
	case storagePoolVolumeTypeImage:
		// allowed
	default:
		return response.BadRequest(fmt.Errorf("storage volumes of type \"%s\" cannot be deleted with the storage api", volumeTypeName))
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, poolName, volumeName, volumeTypeName)
	if err != nil {
		return response.SmartError(err)
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 ||
			volumeType != storagePoolVolumeTypeImage ||
			volumeUsedBy[0] != fmt.Sprintf(
				"/%s/images/%s",
				version.APIVersion,
				volumeName) {
			return response.BadRequest(fmt.Errorf("The storage volume is still in use"))
		}
	}

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.SmartError(err)
		}

		switch volumeType {
		case storagePoolVolumeTypeCustom:
			err = pool.DeleteCustomVolume(volumeName, nil)
		case storagePoolVolumeTypeImage:
			err = pool.DeleteImage(volumeName, nil)
		default:
			return response.BadRequest(fmt.Errorf(`Storage volumes of type "%s" cannot be deleted with the storage api`, volumeTypeName))
		}
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		s, err := storagePoolVolumeInit(d.State(), project, poolName, volumeName, volumeType)
		if err != nil {
			return response.NotFound(err)
		}

		switch volumeType {
		case storagePoolVolumeTypeCustom:
			var snapshots []db.StorageVolumeArgs

			// Delete storage volume snapshots
			snapshots, err = d.cluster.StoragePoolVolumeSnapshotsGetType(volumeName, volumeType, poolID)
			if err != nil {
				return response.SmartError(err)
			}

			for _, snapshot := range snapshots {
				s, err := storagePoolVolumeInit(d.State(), project, poolName, snapshot.Name, volumeType)
				if err != nil {
					return response.NotFound(err)
				}

				err = s.StoragePoolVolumeSnapshotDelete()
				if err != nil {
					return response.SmartError(err)
				}
			}

			err = s.StoragePoolVolumeDelete()
		case storagePoolVolumeTypeImage:
			err = s.ImageDelete(volumeName)
		default:
			return response.BadRequest(fmt.Errorf(`Storage volumes of type "%s" cannot be deleted with the storage api`, volumeTypeName))
		}
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeTypeContainerDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "container")
}

func storagePoolVolumeTypeVMDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "custom")
}

func storagePoolVolumeTypeImageDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "image")
}
