package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/grant-he/lxd/lxd/backup"
	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/instance"
	"github.com/grant-he/lxd/lxd/operations"
	"github.com/grant-he/lxd/lxd/project"
	"github.com/grant-he/lxd/lxd/response"
	"github.com/grant-he/lxd/lxd/revert"
	"github.com/grant-he/lxd/lxd/state"
	storagePools "github.com/grant-he/lxd/lxd/storage"
	"github.com/grant-he/lxd/lxd/util"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
	log "github.com/grant-he/lxd/shared/log15"
	"github.com/grant-he/lxd/shared/logger"
	"github.com/grant-he/lxd/shared/version"
	"github.com/pkg/errors"
)

var storagePoolVolumesCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesTypeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumesTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeContainerCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/container/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeContainerDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeContainerGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeContainerPatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeContainerPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeContainerPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeVMCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/virtual-machine/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeVMDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeVMGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeVMPatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeVMPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeVMPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeCustomCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/custom/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeCustomDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeCustomPatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeCustomPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeImageCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/image/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeImageDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeImageGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeImagePatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeImagePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeImagePut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

// /1.0/storage-pools/{name}/volumes
// List all storage volumes attached to a given storage pool.
func storagePoolVolumesGet(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)

	poolName := mux.Vars(r)["name"]

	recursion := util.IsRecursionRequest(r)

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get all instance volumes currently attached to the storage pool by ID of the pool and project.
	volumes, err := d.cluster.GetStoragePoolVolumes(projectName, poolID, supportedVolumeTypesInstances)
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	// The project name used for custom volumes varies based on whether the project has the
	// featues.storage.volumes feature enabled.
	customVolProjectName, err := project.StorageVolumeProject(d.State().Cluster, projectName, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	// Get all custom volumes currently attached to the storage pool by ID of the pool and project.
	custVolumes, err := d.cluster.GetStoragePoolVolumes(customVolProjectName, poolID, []int{db.StoragePoolVolumeTypeCustom})
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	for _, volume := range custVolumes {
		volumes = append(volumes, volume)
	}

	// We exclude volumes of type image, since those are special: they are stored using the storage_volumes
	// table, but are effectively a cache which is not tied to projects, so we always link the to the default
	// project. This means that we want to filter image volumes and return only the ones that have fingerprint
	// matching images actually in use by the project.
	imageVolumes, err := d.cluster.GetStoragePoolVolumes(project.Default, poolID, []int{db.StoragePoolVolumeTypeImage})
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	projectImages, err := d.cluster.GetImagesFingerprints(projectName, false)
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
		if !recursion {
			volName, snapName, ok := shared.InstanceGetParentAndSnapshotName(volume.Name)
			if ok {
				if projectName == project.Default {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s",
							version.APIVersion, poolName, volume.Type, volName, snapName))
				} else {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s?project=%s",
							version.APIVersion, poolName, volume.Type, volName, snapName, projectName))
				}
			} else {
				if projectName == project.Default {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s",
							version.APIVersion, poolName, volume.Type, volume.Name))
				} else {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s?project=%s",
							version.APIVersion, poolName, volume.Type, volume.Name, projectName))
				}
			}
		} else {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, volume)
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
	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName := mux.Vars(r)["name"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	recursion := util.IsRecursionRequest(r)

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volumes of a given volume type currently attached to the storage pool.
	volumes, err := d.cluster.GetLocalStoragePoolVolumesWithType(projectName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolume{}
	for _, volume := range volumes {
		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, volumeTypeName, volume))
		} else {
			_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volume, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, vol)
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
	poolName := mux.Vars(r)["name"]

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// If we're getting binary content, process separately.
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		return createStoragePoolVolumeFromBackup(d, projectName, r.Body, poolName, r.Header.Get("X-LXD-name"))
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
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

	// Backward compatibility.
	if req.ContentType == "" {
		req.ContentType = "filesystem"
	}

	_, err = storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid content type %q", req.ContentType))
	}

	req.Type = mux.Vars(r)["type"]

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf(`Currently not allowed to create storage volumes of type %q`, req.Type))
	}

	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.cluster.GetLocalStoragePoolVolume(projectName, req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return project.AllowVolumeCreation(tx, projectName, req)
	})
	if err != nil {
		return response.SmartError(err)
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(d, projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func doVolumeCreateOrCopy(d *Daemon, projectName, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeDBContentType, err := storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeDBContentTypeToContentType(volumeDBContentType)
	if err != nil {
		return response.SmartError(err)
	}

	run = func(op *operations.Operation) error {
		if req.Source.Name == "" {
			return pool.CreateCustomVolume(projectName, req.Name, req.Description, req.Config, contentType, op)
		}

		return pool.CreateCustomVolumeFromCopy(projectName, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, req.Source.VolumeOnly, op)
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
	resp := forwardedResponseIfTargetIsRemote(d, r)
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

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf(`Currently not allowed to create storage volumes of type %q`, req.Type))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	poolName := mux.Vars(r)["name"]
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.cluster.GetLocalStoragePoolVolume(projectName, req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(d, projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func doVolumeMigration(d *Daemon, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
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

	sink, err := newStorageMigrationSink(&migrationArgs)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, req.Name)}

	run := func(op *operations.Operation) error {
		// And finally run the migration.
		err = sink.DoStorage(d.State(), projectName, poolName, req, op)
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
	srcPoolName := mux.Vars(r)["pool"]

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
	if volumeTypeName != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Renaming storage volumes of type %q is not allowed", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
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

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, srcPoolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		return storagePoolVolumeTypePostMigration(d.State(), projectName, srcPoolName, volumeName, req)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	var targetPoolID int64
	if req.Pool != "" {
		targetPoolID, err = d.cluster.GetStoragePoolID(req.Pool)
	} else {
		targetPoolID, err = d.cluster.GetStoragePoolID(srcPoolName)
	}
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the name isn't already in use.
	_, err = d.cluster.GetStoragePoolNodeVolumeID(projectName, req.Name, volumeType, targetPoolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.InternalError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	// Check if the daemon itself is using it.
	used, err := storagePools.VolumeUsedByDaemon(d.State(), srcPoolName, volumeName)
	if err != nil {
		return response.SmartError(err)
	}

	if used {
		return response.SmartError(fmt.Errorf("Volume is used by LXD itself and cannot be renamed"))
	}

	// Load source volume.
	srcPoolID, err := d.cluster.GetStoragePoolID(srcPoolName)
	if err != nil {
		return response.SmartError(err)
	}

	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, srcPoolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if a running instance is using it.
	err = storagePools.VolumeUsedByInstanceDevices(d.State(), srcPoolName, projectName, vol, true, func(dbInst db.Instance, project api.Project, profiles []api.Profile, usedByDevices []string) error {
		inst, err := instance.Load(d.State(), db.InstanceToArgs(&dbInst), profiles)
		if err != nil {
			return err
		}

		if inst.IsRunning() {
			return fmt.Errorf("Volume is still in use by running instances")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Detect a rename request.
	if req.Pool == "" || req.Pool == srcPoolName {
		return storagePoolVolumeTypePostRename(d, srcPoolName, projectName, vol, req)
	}

	// Otherwise this is a move request.
	return storagePoolVolumeTypePostMove(d, srcPoolName, projectName, vol, req)
}

// storagePoolVolumeTypePostMigration handles volume migration type POST requests.
func storagePoolVolumeTypePostMigration(state *state.State, projectName, poolName, volumeName string, req api.StorageVolumePost) response.Response {
	ws, err := newStorageMigrationSource(req.VolumeOnly)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, volumeName)}

	run := func(op *operations.Operation) error {
		return ws.DoStorage(state, projectName, poolName, volumeName, op)
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
func storagePoolVolumeTypePostRename(d *Daemon, poolName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	revert := revert.New()
	defer revert.Fail()

	// Update devices using the volume in instances and profiles.
	err = storagePoolVolumeUpdateUsers(d, projectName, pool.Name(), vol, pool.Name(), &newVol)
	if err != nil {
		return response.SmartError(err)
	}

	err = pool.RenameCustomVolume(projectName, vol.Name, req.Name, nil)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()
	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s", version.APIVersion, pool.Name(), db.StoragePoolVolumeTypeNameCustom))
}

// storagePoolVolumeTypePostMove handles volume move type POST requests.
func storagePoolVolumeTypePostMove(d *Daemon, poolName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	newPool, err := storagePools.GetPoolByName(d.State(), req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	run := func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		// Update devices using the volume in instances and profiles.
		err = storagePoolVolumeUpdateUsers(d, projectName, pool.Name(), vol, newPool.Name(), &newVol)
		if err != nil {
			return err
		}
		revert.Add(func() { storagePoolVolumeUpdateUsers(d, projectName, newPool.Name(), &newVol, pool.Name(), vol) })

		// Provide empty description and nil config to instruct CreateCustomVolumeFromCopy to copy it
		// from source volume.
		err = newPool.CreateCustomVolumeFromCopy(projectName, newVol.Name, "", nil, pool.Name(), vol.Name, false, op)
		if err != nil {
			return err
		}

		err = pool.DeleteCustomVolume(projectName, vol.Name, op)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeMove, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeTypeContainerPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, db.StoragePoolVolumeTypeNameContainer)
}

func storagePoolVolumeTypeVMPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, db.StoragePoolVolumeTypeNameVM)
}

func storagePoolVolumeTypeCustomPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, db.StoragePoolVolumeTypeNameCustom)
}

func storagePoolVolumeTypeImagePost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, db.StoragePoolVolumeTypeNameImage)
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
	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the ID of the storage pool the storage volume is supposed to be attached to.
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the storage volume.
	_, volume, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, volume)
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
	projectName := projectParam(r)

	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, err = project.StorageVolumeProject(d.State().Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, pool.Name(), projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
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

	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Possibly check if project limits are honored.
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			return project.AllowVolumeUpdate(tx, projectName, volumeName, req, vol.Config)
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Restore custom volume from snapshot if requested. This should occur first
		// before applying config changes so that changes are applied to the
		// restored volume.
		if req.Restore != "" {
			err = pool.RestoreCustomVolume(projectName, vol.Name, req.Restore, nil)
			if err != nil {
				return response.SmartError(err)
			}
		}

		// Handle custom volume update requests.
		// Only apply changes during a snapshot restore if a non-nil config is supplied to avoid clearing
		// the volume's config if only restoring snapshot.
		if req.Config != nil || req.Restore == "" {
			err = pool.UpdateCustomVolume(projectName, vol.Name, req.Description, req.Config, nil)
			if err != nil {
				return response.SmartError(err)
			}
		}
	} else if volumeType == db.StoragePoolVolumeTypeContainer || volumeType == db.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, vol.Name)
		if err != nil {
			return response.NotFound(err)
		}

		// There is a bug in the lxc client (lxc/storage_volume.go#L829-L865) which
		// means that modifying an instance snapshot's description gets routed here
		// rather than the dedicated snapshot editing route. So need to handle
		// snapshot volumes here too.
		if inst.IsSnapshot() {
			// Handle instance snapshot volume update requests.
			err = pool.UpdateInstanceSnapshot(inst, req.Description, req.Config, nil)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			// Handle instance volume update requests.
			err = pool.UpdateInstance(inst, req.Description, req.Config, nil)
			if err != nil {
				return response.SmartError(err)
			}
		}
	} else if volumeType == db.StoragePoolVolumeTypeImage {
		// Handle image update requests.
		err = pool.UpdateImage(vol.Name, req.Description, req.Config, nil)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		return response.SmartError(fmt.Errorf("Invalid volume type"))
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
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is custom.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, pool.Name(), projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
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

	err = pool.UpdateCustomVolume(projectName, vol.Name, req.Description, req.Config, nil)
	if err != nil {
		return response.SmartError(err)
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
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %q", volumeName))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	if volumeType != db.StoragePoolVolumeTypeCustom && volumeType != db.StoragePoolVolumeTypeImage {
		return response.BadRequest(fmt.Errorf("Storage volumes of type %q cannot be deleted with the storage API", volumeTypeName))
	}

	// Get the storage pool the storage volume is supposed to be attached to.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the storage volume.
	_, volume, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, volume)
	if err != nil {
		return response.SmartError(err)
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 || volumeType != db.StoragePoolVolumeTypeImage || volumeUsedBy[0] != fmt.Sprintf("/%s/images/%s", version.APIVersion, volumeName) {
			return response.BadRequest(fmt.Errorf("The storage volume is still in use"))
		}
	}

	switch volumeType {
	case db.StoragePoolVolumeTypeCustom:
		err = pool.DeleteCustomVolume(projectName, volumeName, nil)
	case db.StoragePoolVolumeTypeImage:
		err = pool.DeleteImage(volumeName, nil)
	default:
		return response.BadRequest(fmt.Errorf(`Storage volumes of type %q cannot be deleted with the storage API`, volumeTypeName))
	}
	if err != nil {
		return response.SmartError(err)
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

func createStoragePoolVolumeFromBackup(d *Daemon, project string, data io.Reader, pool string, volName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Create temporary file to store uploaded backup data.
	backupFile, err := ioutil.TempFile(shared.VarPath("backups"), fmt.Sprintf("%s_", backup.WorkingDirPrefix))
	if err != nil {
		return response.InternalError(err)
	}
	defer os.Remove(backupFile.Name())
	revert.Add(func() { backupFile.Close() })

	// Stream uploaded backup data into temporary file.
	_, err = io.Copy(backupFile, data)
	if err != nil {
		return response.InternalError(err)
	}

	// Detect squashfs compression and convert to tarball.
	backupFile.Seek(0, 0)
	_, algo, decomArgs, err := shared.DetectCompressionFile(backupFile)
	if err != nil {
		return response.InternalError(err)
	}

	if algo == ".squashfs" {
		// Pass the temporary file as program argument to the decompression command.
		decomArgs := append(decomArgs, backupFile.Name())

		// Create temporary file to store the decompressed tarball in.
		tarFile, err := ioutil.TempFile(shared.VarPath("backups"), fmt.Sprintf("%s_decompress_", backup.WorkingDirPrefix))
		if err != nil {
			return response.InternalError(err)
		}
		defer os.Remove(tarFile.Name())

		// Decompress to tarData temporary file.
		err = shared.RunCommandWithFds(nil, tarFile, decomArgs[0], decomArgs[1:]...)
		if err != nil {
			return response.InternalError(err)
		}

		// We don't need the original squashfs file anymore.
		backupFile.Close()
		os.Remove(backupFile.Name())

		// Replace the backup file handle with the handle to the tar file.
		backupFile = tarFile
	}

	// Parse the backup information.
	backupFile.Seek(0, 0)
	logger.Debug("Reading backup file info")
	bInfo, err := backup.GetInfo(backupFile)
	if err != nil {
		return response.BadRequest(err)
	}
	bInfo.Project = project

	// Override pool.
	if pool != "" {
		bInfo.Pool = pool
	}

	// Override volume name.
	if volName != "" {
		bInfo.Name = volName
	}

	logger.Debug("Backup file info loaded", log.Ctx{
		"type":      bInfo.Type,
		"name":      bInfo.Name,
		"project":   bInfo.Project,
		"backend":   bInfo.Backend,
		"pool":      bInfo.Pool,
		"optimized": *bInfo.OptimizedStorage,
		"snapshots": bInfo.Snapshots,
	})

	// Check storage pool exists.
	_, _, err = d.State().Cluster.GetStoragePoolInAnyState(bInfo.Pool)
	if errors.Cause(err) == db.ErrNoSuchObject {
		// The storage pool doesn't exist. If backup is in binary format (so we cannot alter
		// the backup.yaml) or the pool has been specified directly from the user restoring
		// the backup then we cannot proceed so return an error.
		if *bInfo.OptimizedStorage || pool != "" {
			return response.InternalError(errors.Wrap(err, "Storage pool not found"))
		}

		// Otherwise try and restore to the project's default profile pool.
		_, profile, err := d.State().Cluster.GetProfile(bInfo.Project, "default")
		if err != nil {
			return response.InternalError(errors.Wrap(err, "Failed to get default profile"))
		}

		_, v, err := shared.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return response.InternalError(errors.Wrap(err, "Failed to get root disk device"))
		}

		// Use the default-profile's root pool.
		bInfo.Pool = v["pool"]
	} else if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer backupFile.Close()
		defer runRevert.Fail()

		pool, err := storagePools.GetPoolByName(d.State(), bInfo.Pool)
		if err != nil {
			return err
		}

		// Check if the backup is optimized that the source pool driver matches the target pool driver.
		if *bInfo.OptimizedStorage && pool.Driver().Info().Name != bInfo.Backend {
			return fmt.Errorf("Optimized backup storage driver %q differs from the target storage pool driver %q", bInfo.Backend, pool.Driver().Info().Name)
		}

		// Dump tarball to storage.
		err = pool.CreateCustomVolumeFromBackup(*bInfo, backupFile, nil)
		if err != nil {
			return errors.Wrap(err, "Create custom volume from backup")
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{bInfo.Name}

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationCustomVolumeBackupRestore, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}
