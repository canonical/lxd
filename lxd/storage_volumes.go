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

var storagePoolVolumesCmd = Command{
	name: "storage-pools/{name}/volumes",
	get:  storagePoolVolumesGet,
	post: storagePoolVolumesPost,
}

var storagePoolVolumesTypeCmd = Command{
	name: "storage-pools/{name}/volumes/{type}",
	get:  storagePoolVolumesTypeGet,
	post: storagePoolVolumesTypePost,
}

var storagePoolVolumeTypeContainerCmd = Command{
	name:   "storage-pools/{pool}/volumes/container/{name:.*}",
	post:   storagePoolVolumeTypeContainerPost,
	get:    storagePoolVolumeTypeContainerGet,
	put:    storagePoolVolumeTypeContainerPut,
	patch:  storagePoolVolumeTypeContainerPatch,
	delete: storagePoolVolumeTypeContainerDelete,
}

var storagePoolVolumeTypeCustomCmd = Command{
	name:   "storage-pools/{pool}/volumes/custom/{name}",
	post:   storagePoolVolumeTypeCustomPost,
	get:    storagePoolVolumeTypeCustomGet,
	put:    storagePoolVolumeTypeCustomPut,
	patch:  storagePoolVolumeTypeCustomPatch,
	delete: storagePoolVolumeTypeCustomDelete,
}

var storagePoolVolumeTypeImageCmd = Command{
	name:   "storage-pools/{pool}/volumes/image/{name}",
	post:   storagePoolVolumeTypeImagePost,
	get:    storagePoolVolumeTypeImageGet,
	put:    storagePoolVolumeTypeImagePut,
	patch:  storagePoolVolumeTypeImagePatch,
	delete: storagePoolVolumeTypeImageDelete,
}

// /1.0/storage-pools/{name}/volumes
// List all storage volumes attached to a given storage pool.
func storagePoolVolumesGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	poolName := mux.Vars(r)["name"]

	recursion := util.IsRecursionRequest(r)

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
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
		return SmartError(err)
	}

	imageVolumes, err := d.cluster.StoragePoolVolumesGet("default", poolID, []int{storagePoolVolumeTypeImage})
	if err != nil && err != db.ErrNoSuchObject {
		return SmartError(err)
	}

	projectImages, err := d.cluster.ImagesGet(project, false)
	if err != nil {
		return SmartError(err)
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
			return InternalError(err)
		}

		if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
			apiEndpoint = "container"
		} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
			apiEndpoint = "image"
		}

		if !recursion {
			volName, snapName, ok := containerGetParentAndSnapshotName(volume.Name)
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
			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, volume.Name, volume.Type)
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
	project := projectParam(r)

	// Get the name of the pool the storage volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["name"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	recursion := util.IsRecursionRequest(r)

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

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, vol.Name, vol.Type)
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

		if req.Source.VolumeOnly {
			return nil
		}

		if req.Source.Pool == "" {
			return nil
		}

		// Convert the volume type name to our internal integer representation.
		volumeType, err := storagePoolVolumeTypeNameToType(req.Type)
		if err != nil {
			return err
		}

		// Get poolID of source pool
		poolID, err := d.cluster.StoragePoolGetID(req.Source.Pool)
		if err != nil {
			return err
		}

		// Get volumes attached to source storage volume
		volumes, err := d.cluster.StoragePoolVolumeSnapshotsGetType(req.Source.Name, volumeType, poolID)
		if err != nil {
			return err
		}

		for _, vol := range volumes {
			_, snapshotName, _ := containerGetParentAndSnapshotName(vol)

			copyReq := api.StorageVolumesPost{}
			copyReq.Name = fmt.Sprintf("%s%s%s", req.Name, shared.SnapshotDelimiter, snapshotName)
			copyReq.Type = "custom"
			copyReq.Source.Name = fmt.Sprintf("%s%s%s", req.Source.Name, shared.SnapshotDelimiter, snapshotName)
			copyReq.Source.Pool = req.Source.Pool

			err = storagePoolVolumeSnapshotCreateInternal(d.State(), poolName, &copyReq)
			if err != nil {
				return err
			}
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

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeCopy, nil, nil, run, nil, nil)
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
		op, err = operationCreate(d.cluster, "", operationClassWebsocket, db.OperationVolumeCreate, resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return InternalError(err)
		}
	} else {
		op, err = operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeCopy, resources, nil, run, nil, nil)
		if err != nil {
			return InternalError(err)
		}
	}

	return OperationResponse(op)
}

// /1.0/storage-pools/{name}/volumes/{type}/{name}
// Rename a storage volume of a given volume type in a given storage pool.
func storagePoolVolumeTypePost(d *Daemon, r *http.Request, volumeTypeName string) Response {
	// Get the name of the storage volume.
	var volumeName string
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2])
	} else if len(fields) > 1 {
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1])
	} else if len(fields) > 0 {
		// Handle volume
		volumeName = fields[0]
	} else {
		return BadRequest(fmt.Errorf("invalid storage volume %s", mux.Vars(r)["name"]))
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

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

	s, err := storagePoolVolumeInit(d.State(), "default", poolName, volumeName, storagePoolVolumeTypeCustom)
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

			op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeMigrate, resources, nil, ws.DoStorage, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(d.cluster, "", operationClassWebsocket, db.OperationVolumeMigrate, resources, ws.Metadata(), ws.DoStorage, nil, ws.Connect)
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

			// Rename volume snapshots
			// Get volumes attached to source storage volume
			volumes, err := d.cluster.StoragePoolVolumeSnapshotsGetType(volumeName,
				storagePoolVolumeTypeCustom, poolID)
			if err != nil {
				return err
			}

			for _, vol := range volumes {
				// Rename volume snapshots
				snapshot, err := storagePoolVolumeInit(d.State(), "default", poolName,
					vol, storagePoolVolumeTypeCustom)
				if err != nil {
					return err
				}

				dstVolumeName, dstSnapshotName, _ := containerGetParentAndSnapshotName(req.Name)

				moveReq := api.StorageVolumesPost{}
				moveReq.Name = fmt.Sprintf("%s%s%s", dstVolumeName, shared.SnapshotDelimiter, dstSnapshotName)
				moveReq.Type = "custom"
				moveReq.Source.Name = vol
				moveReq.Source.Pool = poolName

				err = storagePoolVolumeSnapshotCreateInternal(d.State(), req.Pool, &moveReq)
				if err != nil {
					return err
				}

				err = snapshot.StoragePoolVolumeSnapshotDelete()
				if err != nil {
					return err
				}
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

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeMove, nil, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func storagePoolVolumeTypeContainerPost(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePost(d, r, "container")
}

func storagePoolVolumeTypeCustomPost(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePost(d, r, "custom")
}

func storagePoolVolumeTypeImagePost(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePost(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// Get storage volume of a given volume type on a given storage pool.
func storagePoolVolumeTypeGet(d *Daemon, r *http.Request, volumeTypeName string) Response {
	project := projectParam(r)

	// Get the name of the storage volume.
	var volumeName string
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2])
	} else if len(fields) > 1 {
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1])
	} else if len(fields) > 0 {
		// Handle volume
		volumeName = fields[0]
	} else {
		return BadRequest(fmt.Errorf("invalid storage volume %s", mux.Vars(r)["name"]))
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

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

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, volume.Name, volume.Type)
	if err != nil {
		return SmartError(err)
	}
	volume.UsedBy = volumeUsedBy

	etag := []interface{}{volumeName, volume.Type, volume.Config}

	return SyncResponseETag(true, volume, etag)
}

func storagePoolVolumeTypeContainerGet(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypeGet(d, r, "container")
}

func storagePoolVolumeTypeCustomGet(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypeGet(d, r, "custom")
}

func storagePoolVolumeTypeImageGet(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypeGet(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePut(d *Daemon, r *http.Request, volumeTypeName string) Response {
	// Get the name of the storage volume.
	var volumeName string
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2])
	} else if len(fields) > 1 {
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1])
	} else if len(fields) > 0 {
		// Handle volume
		volumeName = fields[0]
	} else {
		return BadRequest(fmt.Errorf("invalid storage volume %s", mux.Vars(r)["name"]))
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

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
	etag := []interface{}{volumeName, volume.Type, volume.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Restore != "" {
		ctsUsingVolume, err := storagePoolVolumeUsedByRunningContainersWithProfilesGet(d.State(), poolName, volume.Name, storagePoolVolumeTypeNameCustom, true)
		if err != nil {
			return InternalError(err)
		}

		if len(ctsUsingVolume) != 0 {
			return BadRequest(fmt.Errorf("Cannot restore custom volume used by running containers"))
		}

		err = storagePoolVolumeRestore(d.State(), poolName, volumeName, volumeType, req.Restore)
		if err != nil {
			return SmartError(err)
		}
	} else {
		// Validate the configuration
		err = storageVolumeValidateConfig(volumeName, req.Config, pool)
		if err != nil {
			return BadRequest(err)
		}

		err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
		if err != nil {
			return SmartError(err)
		}
	}

	return EmptySyncResponse
}

func storagePoolVolumeTypeContainerPut(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePut(d, r, "container")
}

func storagePoolVolumeTypeCustomPut(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePut(d, r, "custom")
}

func storagePoolVolumeTypeImagePut(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePut(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePatch(d *Daemon, r *http.Request, volumeTypeName string) Response {
	// Get the name of the storage volume.
	var volumeName string
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2])
	} else if len(fields) > 1 {
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1])
	} else if len(fields) > 0 {
		// Handle volume
		volumeName = fields[0]
	} else {
		return BadRequest(fmt.Errorf("invalid storage volume %s", mux.Vars(r)["name"]))
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

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
	etag := []interface{}{volumeName, volume.Type, volume.Config}

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

func storagePoolVolumeTypeContainerPatch(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePatch(d, r, "container")
}

func storagePoolVolumeTypeCustomPatch(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePatch(d, r, "custom")
}

func storagePoolVolumeTypeImagePatch(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypePatch(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypeDelete(d *Daemon, r *http.Request, volumeTypeName string) Response {
	project := projectParam(r)

	// Get the name of the storage volume.
	var volumeName string
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots
		volumeName = fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2])
	} else if len(fields) > 0 {
		// Handle volume
		volumeName = fields[0]
	} else {
		return BadRequest(fmt.Errorf("invalid storage volume %s", mux.Vars(r)["name"]))
	}

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

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

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), project, volumeName, volumeTypeName)
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

	s, err := storagePoolVolumeInit(d.State(), "default", poolName, volumeName, volumeType)
	if err != nil {
		return NotFound(err)
	}

	switch volumeType {
	case storagePoolVolumeTypeCustom:
		var snapshots []string

		// Delete storage volume snapshots
		snapshots, err = d.cluster.StoragePoolVolumeSnapshotsGetType(volumeName, volumeType, poolID)
		if err != nil {
			return SmartError(err)
		}

		for _, snapshot := range snapshots {
			s, err := storagePoolVolumeInit(d.State(), project, poolName, snapshot, volumeType)
			if err != nil {
				return NotFound(err)
			}

			err = s.StoragePoolVolumeSnapshotDelete()
			if err != nil {
				return SmartError(err)
			}
		}

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

func storagePoolVolumeTypeContainerDelete(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypeDelete(d, r, "container")
}

func storagePoolVolumeTypeCustomDelete(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypeDelete(d, r, "custom")
}

func storagePoolVolumeTypeImageDelete(d *Daemon, r *http.Request) Response {
	return storagePoolVolumeTypeDelete(d, r, "image")
}
