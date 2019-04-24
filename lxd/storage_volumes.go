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
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

var storagePoolVolumesCmd = APIEndpoint{
	Name: "storage-pools/{name}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Name: "storage-pools/{name}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesTypeGet},
	Post: APIEndpointAction{Handler: storagePoolVolumesTypePost},
}

var storagePoolVolumeTypeCmd = APIEndpoint{
	Name: "storage-pools/{pool}/volumes/{type}/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeDelete},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeGet},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypePatch},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypePost},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypePut},
}

// /1.0/storage-pools/{name}/volumes
// List all storage volumes attached to a given storage pool.
func storagePoolVolumesGet(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	recursion := util.IsRecursionRequest(r)

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get all volumes currently attached to the storage pool by ID of the
	// pool.
	volumes, err := d.cluster.StoragePoolVolumesGet(poolID, supportedVolumeTypes)
	if err != nil && err != db.ErrNoSuchObject {
		return SmartError(err)
	}

	resultString := []string{}
	for _, volume := range volumes {
		apiEndpoint, err := storagePoolVolumeTypeNameToAPIEndpoint(volume.Type)
		if err != nil {
			return InternalError(err)
		}

		if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
			apiEndpoint = "container"
		} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
			apiEndpoint = "image"
		}

		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, apiEndpoint, volume.Name))
		} else {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), volume.Name, volume.Type)
			if err != nil {
				return InternalError(err)
			}
			volume.UsedBy = volumeUsedBy
		}
	}

	if !recursion {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, volumes)
}

// /1.0/storage-pools/{name}/volumes/{type}
// List all storage volumes of a given volume type for a given storage pool.
func storagePoolVolumesTypeGet(d *Daemon, r *http.Request) Response {
	// Get the name of the pool the storage volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["name"]

	recursion := util.IsRecursionRequest(r)

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get the names of all storage volumes of a given volume type currently
	// attached to the storage pool.
	volumes, err := d.cluster.StoragePoolNodeVolumesGetType(volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolume{}
	for _, volume := range volumes {
		if !recursion {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return InternalError(err)
			}

			if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
				apiEndpoint = "container"
			} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
				apiEndpoint = "image"
			}

			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, apiEndpoint, volume))
		} else {
			_, vol, err := d.cluster.StoragePoolNodeVolumeGetType(volume, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), vol.Name, vol.Type)
			if err != nil {
				return SmartError(err)
			}
			vol.UsedBy = volumeUsedBy

			resultMap = append(resultMap, vol)
		}
	}

	if !recursion {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume in a given storage pool.
func storagePoolVolumesTypePost(d *Daemon, r *http.Request) Response {
	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	req.Type = mux.Vars(r)["type"]

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if req.Type != storagePoolVolumeTypeNameCustom {
		return BadRequest(fmt.Errorf(`Currently not allowed to create `+
			`storage volumes of type %s`, req.Type))
	}

	poolName := mux.Vars(r)["name"]

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "migration":
		return doVolumeMigration(d, poolName, &req)
	default:
		return BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}
}

func doVolumeCreateOrCopy(d *Daemon, poolName string, req *api.StorageVolumesPost) Response {
	doWork := func() error {
		err := storagePoolVolumeCreateInternal(d.State(), poolName, req)
		if err != nil {
			return err
		}
		return nil
	}

	if req.Source.Name == "" {
		err := doWork()
		if err != nil {
			return SmartError(err)
		}

		return EmptySyncResponse
	}

	run := func(op *operation) error {
		return doWork()
	}

	op, err := operationCreate(d.cluster, operationClassTask, "Copying storage volume", nil, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)

}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume of a given volume type in a given storage pool.
func storagePoolVolumesPost(d *Daemon, r *http.Request) Response {
	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Check that the user gave use a storage volume type for the storage
	// volume we are about to create.
	if req.Type == "" {
		return BadRequest(fmt.Errorf("You must provide a storage volume type of the storage volume"))
	}

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if req.Type != storagePoolVolumeTypeNameCustom {
		return BadRequest(fmt.Errorf(`Currently not allowed to create `+
			`storage volumes of type %s`, req.Type))
	}

	poolName := mux.Vars(r)["name"]

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, poolName, &req)
	case "migration":
		return doVolumeMigration(d, poolName, &req)
	default:
		return BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}
}

func doVolumeMigration(d *Daemon, poolName string, req *api.StorageVolumesPost) Response {
	// Validate migration mode
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return NotImplemented(fmt.Errorf("Mode '%s' not implemented", req.Source.Mode))
	}

	storage, err := storagePoolVolumeDBCreateInternal(d.State(), poolName, req)
	if err != nil {
		return InternalError(err)
	}

	// create new certificate
	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			return InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	migrationArgs := MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer},
		Secrets: req.Source.Websockets,
		Push:    push,
		Storage: storage,
	}

	sink, err := NewStorageMigrationSink(&migrationArgs)
	if err != nil {
		return InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, req.Name)}

	run := func(op *operation) error {
		// And finally run the migration.
		err = sink.DoStorage(op)
		if err != nil {
			logger.Error("Error during migration sink", log.Ctx{"err": err})
			return fmt.Errorf("Error transferring storage volume: %s", err)
		}

		return nil
	}

	var op *operation
	if push {
		op, err = operationCreate(d.cluster, operationClassWebsocket, "Creating storage volume", resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return InternalError(err)
		}
	} else {
		op, err = operationCreate(d.cluster, operationClassTask, "Copying storage volume", resources, nil, run, nil, nil)
		if err != nil {
			return InternalError(err)
		}
	}

	return OperationResponse(op)
}

// /1.0/storage-pools/{name}/volumes/{type}/{name}
// Rename a storage volume of a given volume type in a given storage pool.
func storagePoolVolumeTypePost(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	req := api.StorageVolumePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if volumeTypeName != storagePoolVolumeTypeNameCustom {
		return BadRequest(fmt.Errorf("Renaming storage volumes of type %s is not allowed", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	var poolID int64
	if req.Pool != "" {
		poolID, err = d.cluster.StoragePoolGetID(req.Pool)
	} else {
		poolID, err = d.cluster.StoragePoolGetID(poolName)
	}
	if err != nil {
		return SmartError(err)
	}

	// We need to restore the body of the request since it has already been
	// read, and if we forwarded it now no body would be written out.
	buf := bytes.Buffer{}
	err = json.NewEncoder(&buf).Encode(req)
	if err != nil {
		return SmartError(err)
	}
	r.Body = shared.BytesReadCloser{Buf: &buf}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}

	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if response != nil {
		return response
	}

	s, err := storagePoolVolumeInit(d.State(), poolName, volumeName, storagePoolVolumeTypeCustom)
	if err != nil {
		return InternalError(err)
	}

	// This is a migration request so send back requested secrets
	if req.Migration {
		ws, err := NewStorageMigrationSource(s)
		if err != nil {
			return InternalError(err)
		}

		resources := map[string][]string{}
		resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, volumeName)}

		if req.Target != nil {
			// Push mode
			err := ws.ConnectStorageTarget(*req.Target)
			if err != nil {
				return InternalError(err)
			}

			op, err := operationCreate(d.cluster, operationClassTask, "Migrating storage volume", resources, nil, ws.DoStorage, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(d.cluster, operationClassWebsocket, "Migrating storage volume", resources, ws.Metadata(), ws.DoStorage, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	// Check that the name isn't already in use.
	_, err = d.cluster.StoragePoolNodeVolumeGetTypeID(req.Name,
		storagePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return InternalError(err)
		}

		return Conflict(fmt.Errorf("Name '%s' already in use", req.Name))
	}

	doWork := func() error {
		ctsUsingVolume, err := storagePoolVolumeUsedByRunningContainersWithProfilesGet(d.State(), poolName, volumeName, storagePoolVolumeTypeNameCustom, true)
		if err != nil {
			return err
		}
		if len(ctsUsingVolume) > 0 {
			return fmt.Errorf("Volume is still in use by running containers")
		}

		err = storagePoolVolumeUpdateUsers(d, poolName, volumeName, req.Pool, req.Name)
		if err != nil {
			return err
		}

		if req.Pool == "" || req.Pool == poolName {
			err := s.StoragePoolVolumeRename(req.Name)
			if err != nil {
				storagePoolVolumeUpdateUsers(d, req.Pool, req.Name, poolName, volumeName)
				return err
			}
		} else {
			moveReq := api.StorageVolumesPost{}
			moveReq.Name = req.Name
			moveReq.Type = "custom"
			moveReq.Source.Name = volumeName
			moveReq.Source.Pool = poolName
			err := storagePoolVolumeCreateInternal(d.State(), req.Pool, &moveReq)
			if err != nil {
				storagePoolVolumeUpdateUsers(d, req.Pool, req.Name, poolName, volumeName)
				return err
			}
			err = s.StoragePoolVolumeDelete()
			if err != nil {
				return err
			}
		}

		return nil
	}

	if req.Pool == "" {
		err = doWork()
		if err != nil {
			return SmartError(err)
		}

		return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s", version.APIVersion, poolName, storagePoolVolumeAPIEndpointCustom))
	}

	run := func(op *operation) error {
		return doWork()
	}

	op, err := operationCreate(d.cluster, operationClassTask, "Moving storage volume", nil, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// Get storage volume of a given volume type on a given storage pool.
func storagePoolVolumeTypeGet(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Get the ID of the storage pool the storage volume is supposed to be
	// attached to.
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if response != nil {
		return response
	}

	// Get the storage volume.
	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), volume.Name, volume.Type)
	if err != nil {
		return SmartError(err)
	}
	volume.UsedBy = volumeUsedBy

	etag := []interface{}{volume.Name, volume.Type, volume.Config}

	return SyncResponseETag(true, volume, etag)
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePut(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	poolID, pool, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if response != nil {
		return response
	}

	// Get the existing storage volume.
	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{volume.Name, volume.Type, volume.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Validate the configuration
	err = storageVolumeValidateConfig(volumeName, req.Config, pool)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePatch(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Get the ID of the storage pool the storage volume is supposed to be
	// attached to.
	poolID, pool, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if response != nil {
		return response
	}

	// Get the existing storage volume.
	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{volume.Name, volume.Type, volume.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range volume.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Validate the configuration
	err = storageVolumeValidateConfig(volumeName, req.Config, pool)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypeDelete(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if response != nil {
		return response
	}

	switch volumeType {
	case storagePoolVolumeTypeCustom:
		// allowed
	case storagePoolVolumeTypeImage:
		// allowed
	default:
		return BadRequest(fmt.Errorf("storage volumes of type \"%s\" cannot be deleted with the storage api", volumeTypeName))
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), volumeName, volumeTypeName)
	if err != nil {
		return SmartError(err)
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 ||
			volumeType != storagePoolVolumeTypeImage ||
			volumeUsedBy[0] != fmt.Sprintf(
				"/%s/images/%s",
				version.APIVersion,
				volumeName) {
			return BadRequest(fmt.Errorf(`The storage volume is ` +
				`still in use by containers or profiles`))
		}
	}

	s, err := storagePoolVolumeInit(d.State(), poolName, volumeName, volumeType)
	if err != nil {
		return NotFound(err)
	}

	switch volumeType {
	case storagePoolVolumeTypeCustom:
		err = s.StoragePoolVolumeDelete()
	case storagePoolVolumeTypeImage:
		err = s.ImageDelete(volumeName)
	default:
		return BadRequest(fmt.Errorf(`Storage volumes of type "%s" `+
			`cannot be deleted with the storage api`,
			volumeTypeName))
	}
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}
