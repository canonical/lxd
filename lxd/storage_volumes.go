package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/archive"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/filter"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumesCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumesTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumePatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumePut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes storage storage_pool_volumes_get
//
// Get the storage volumes
//
// Returns a list of storage volumes (URLs).
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/storage-pools/local/volumes/container/a1",
//               "/1.0/storage-pools/local/volumes/container/a2",
//               "/1.0/storage-pools/local/volumes/custom/backups",
//               "/1.0/storage-pools/local/volumes/custom/images"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{name}/volumes?recursion=1 storage storage_pool_volumes_get_recursion1
//
// Get the storage volumes
//
// Returns a list of storage volumes (structs).
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of storage volumes
//           items:
//             $ref: "#/definitions/StorageVolume"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type} storage storage_pool_volumes_type_get
//
// Get the storage volumes
//
// Returns a list of storage volumes (URLs) (type specific endpoint).
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/storage-pools/local/volumes/custom/backups",
//               "/1.0/storage-pools/local/volumes/custom/images"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}?recursion=1 storage storage_pool_volumes_type_get_recursion1
//
// Get the storage volumes
//
// Returns a list of storage volumes (structs) (type specific endpoint).
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of storage volumes
//           items:
//             $ref: "#/definitions/StorageVolume"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumesGet(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	targetMember := queryParam(r, "target")
	memberSpecific := targetMember != ""

	poolName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert volume type name to internal integer representation if requested.
	var volumeType int
	if volumeTypeName != "" {
		volumeType, err = storagePools.VolumeTypeNameToDBType(volumeTypeName)
		if err != nil {
			return response.BadRequest(err)
		}
	}

	filterStr := r.FormValue("filter")
	var clauses []filter.Clause
	if filterStr != "" {
		var err error
		clauses, err = filter.Parse(filterStr)
		if err != nil {
			return response.SmartError(fmt.Errorf("Invalid filter: %w", err))
		}
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.db.Cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Detect project mode.
	requestProjectName := queryParam(r, "project")
	allProjects := shared.IsTrue(queryParam(r, "all-projects"))

	if allProjects && requestProjectName != "" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects"))
	} else if !allProjects && requestProjectName == "" {
		requestProjectName = project.Default
	}

	var dbVolumes []*db.StorageVolume

	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var customVolProjectName string

		if !allProjects {
			dbProject, err := cluster.GetProject(ctx, tx.Tx(), requestProjectName)
			if err != nil {
				return err
			}

			p, err := dbProject.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// The project name used for custom volumes varies based on whether the
			// project has the featues.storage.volumes feature enabled.
			customVolProjectName = project.StorageVolumeProjectFromRecord(p, db.StoragePoolVolumeTypeCustom)
		}

		filters := make([]db.StorageVolumeFilter, 0)

		for i := range supportedVolumeTypes {
			supportedVolType := supportedVolumeTypes[i] // Local variable for use as pointer below.

			if volumeTypeName != "" && supportedVolType != volumeType {
				continue // Only include the requested type if specified.
			}

			switch supportedVolType {
			case db.StoragePoolVolumeTypeCustom:
				volTypeCustom := db.StoragePoolVolumeTypeCustom
				filter := db.StorageVolumeFilter{
					Type: &volTypeCustom,
				}

				if !allProjects {
					filter.Project = &customVolProjectName
				}

				filters = append(filters, filter)
			case db.StoragePoolVolumeTypeImage:
				// Image volumes are effectively a cache and are always linked to default project.
				// We filter the ones relevant to requested project below after the query has run.
				volTypeImage := db.StoragePoolVolumeTypeImage
				filters = append(filters, db.StorageVolumeFilter{
					Type: &volTypeImage,
				})
			default:
				// Include instance volume types using the specified project.
				filter := db.StorageVolumeFilter{
					Type: &supportedVolType,
				}

				if !allProjects {
					filter.Project = &requestProjectName
				}

				filters = append(filters, filter)
			}
		}

		dbVolumes, err = tx.GetStoragePoolVolumes(poolID, memberSpecific, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage volumes: %w", err)
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	var projectImages []string
	if !allProjects {
		projectImages, err = d.db.Cluster.GetImagesFingerprints(requestProjectName, false)
		if err != nil {
			return response.SmartError(err)
		}
	}

	dbVolumes = filterVolumes(dbVolumes, clauses, allProjects, projectImages)

	// Sort by type then volume name.
	sort.SliceStable(dbVolumes, func(i, j int) bool {
		volA := dbVolumes[i]
		volB := dbVolumes[j]

		if volA.Type != volB.Type {
			return dbVolumes[i].Type < dbVolumes[j].Type
		}

		return volA.Name < volB.Name
	})

	if util.IsRecursionRequest(r) {
		volumes := make([]*api.StorageVolume, 0, len(dbVolumes))
		for _, dbVol := range dbVolumes {
			vol := &dbVol.StorageVolume

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), requestProjectName, poolName, dbVol)
			if err != nil {
				return response.InternalError(err)
			}

			vol.UsedBy = project.FilterUsedBy(r, volumeUsedBy)
			volumes = append(volumes, vol)
		}

		return response.SyncResponse(true, volumes)
	}

	urls := make([]string, 0, len(dbVolumes))
	for _, dbVol := range dbVolumes {
		urls = append(urls, dbVol.StorageVolume.URL(version.APIVersion, poolName).String())
	}

	return response.SyncResponse(true, urls)
}

// filterVolumes returns a filtered list of volumes that match the given clauses.
func filterVolumes(volumes []*db.StorageVolume, clauses []filter.Clause, allProjects bool, filterProjectImages []string) []*db.StorageVolume {
	// FilterStorageVolume is for filtering purpose only.
	// It allows to filter snapshots by using default filter mechanism.
	type FilterStorageVolume struct {
		api.StorageVolume `yaml:",inline"`
		Snapshot          string `yaml:"snapshot"`
	}

	filtered := []*db.StorageVolume{}
	for _, volume := range volumes {
		// Filter out image volumes that are not used by this project.
		if volume.Type == db.StoragePoolVolumeTypeNameImage && !allProjects && !shared.StringInSlice(volume.Name, filterProjectImages) {
			continue
		}

		tmpVolume := FilterStorageVolume{
			StorageVolume: volume.StorageVolume,
			Snapshot:      strconv.FormatBool(strings.Contains(volume.Name, shared.SnapshotDelimiter)),
		}

		if !filter.Match(tmpVolume, clauses) {
			continue
		}

		filtered = append(filtered, volume)
	}

	return filtered
}

// swagger:operation POST /1.0/storage-pools/{name}/volumes/{type} storage storage_pool_volumes_type_post
//
// Add a storage volume
//
// Creates a new storage volume (type specific endpoint).
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: volume
//     description: Storage volume
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumesPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumesTypePost(d *Daemon, r *http.Request) response.Response {
	poolName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	projectName, err := project.StorageVolumeProject(d.State().DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// If we're getting binary content, process separately.
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		return createStoragePoolVolumeFromBackup(d, r, projectParam(r), projectName, r.Body, poolName, r.Header.Get("X-LXD-name"))
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Backward compatibility.
	if req.ContentType == "" {
		req.ContentType = db.StoragePoolVolumeContentTypeNameFS
	}

	_, err = storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.BadRequest(err)
	}

	req.Type, err = url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Currently not allowed to create storage volumes of type %q", req.Type))
	}

	poolID, err := d.db.Cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, vol, err := d.db.Cluster.GetLocalStoragePoolVolume(projectName, req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if !response.IsNotFoundError(err) {
		if err != nil {
			return response.SmartError(err)
		}

		if !req.Source.Refresh {
			return response.Conflict(fmt.Errorf("Volume name %q already exists", req.Name))
		}
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowVolumeCreation(tx, projectName, req)
	})
	if err != nil {
		return response.SmartError(err)
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, r, projectParam(r), projectName, poolName, &req)
	case "copy":
		if vol != nil {
			return doCustomVolumeRefresh(d, r, projectParam(r), projectName, poolName, &req)
		}

		return doVolumeCreateOrCopy(d, r, projectParam(r), projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(d, r, projectParam(r), projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func doCustomVolumeRefresh(d *Daemon, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	run = func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		if req.Source.Name == "" {
			return fmt.Errorf("No source volume name supplied")
		}

		err = pool.RefreshCustomVolume(projectName, req.Source.Project, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, !req.Source.VolumeOnly, op)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(d.State(), requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func doVolumeCreateOrCopy(d *Daemon, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	pool, err := storagePools.LoadByName(d.State(), poolName)
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
			// Use an empty operation for this sync response to pass the requestor
			op := &operations.Operation{}
			op.SetRequestor(r)
			return pool.CreateCustomVolume(projectName, req.Name, req.Description, req.Config, contentType, op)
		}

		return pool.CreateCustomVolumeFromCopy(projectName, req.Source.Project, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, !req.Source.VolumeOnly, op)
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
	op, err := operations.OperationCreate(d.State(), requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/storage-pools/{name}/volumes storage storage_pool_volumes_post
//
// Add a storage volume
//
// Creates a new storage volume.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: volume
//     description: Storage volume
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumesPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
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

	// Quick checks.
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
		return response.BadRequest(fmt.Errorf("Currently not allowed to create storage volumes of type %q", req.Type))
	}

	// Backward compatibility.
	if req.ContentType == "" {
		req.ContentType = db.StoragePoolVolumeContentTypeNameFS
	}

	projectName, err := project.StorageVolumeProject(d.State().DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	poolID, err := d.db.Cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.db.Cluster.GetLocalStoragePoolVolume(projectName, req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if !response.IsNotFoundError(err) {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, r, projectParam(r), projectName, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, r, projectParam(r), projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(d, r, projectParam(r), projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func doVolumeMigration(d *Daemon, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
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
		URL: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig:  config,
			NetDialContext:   shared.RFC3493Dialer,
			HandshakeTimeout: time.Second * 5,
		},
		Secrets:    req.Source.Websockets,
		Push:       push,
		VolumeOnly: req.Source.VolumeOnly,
		Refresh:    req.Source.Refresh,
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
			logger.Error("Error during migration sink", logger.Ctx{"err": err})
			return fmt.Errorf("Error transferring storage volume: %s", err)
		}

		return nil
	}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(d.State(), requestProjectName, operations.OperationClassWebsocket, operationtype.VolumeCreate, resources, sink.Metadata(), run, nil, sink.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(d.State(), requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/storage-pools/{name}/volumes/{type}/{volume} storage storage_pool_volume_type_post
//
// Rename or move/migrate a storage volume
//
// Renames, moves a storage volume between pools or migrates an instance to another server.
//
// The returned operation metadata will vary based on what's requested.
// For rename or move within the same server, this is a simple background operation with progress data.
// For migration, in the push case, this will similarly be a background
// operation with progress data, for the pull case, it will be a websocket
// operation with a number of secrets to be passed to the target server.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: migration
//     description: Migration request
//     schema:
//       $ref: "#/definitions/StorageVolumePost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumePost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	srcPoolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.StorageVolumePost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
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

	projectName, err := project.StorageVolumeProject(d.State().DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	targetProjectName := projectName
	if req.Project != "" {
		targetProjectName = req.Project

		// Check is user has access to target project
		if !rbac.UserHasPermission(r, targetProjectName, "manage-storage-volumes") {
			return response.Forbidden(nil)
		}
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
		return storagePoolVolumeTypePostMigration(d.State(), r, projectParam(r), projectName, srcPoolName, volumeName, req)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	var targetPoolID int64
	if req.Pool != "" {
		targetPoolID, err = d.db.Cluster.GetStoragePoolID(req.Pool)
	} else {
		targetPoolID, err = d.db.Cluster.GetStoragePoolID(srcPoolName)
	}

	if err != nil {
		return response.SmartError(err)
	}

	// Check that the name isn't already in use.
	_, err = d.db.Cluster.GetStoragePoolNodeVolumeID(targetProjectName, req.Name, volumeType, targetPoolID)
	if !response.IsNotFoundError(err) {
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
	srcPoolID, err := d.db.Cluster.GetStoragePoolID(srcPoolName)
	if err != nil {
		return response.SmartError(err)
	}

	_, vol, err := d.db.Cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, srcPoolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if a running instance is using it.
	err = storagePools.VolumeUsedByInstanceDevices(d.State(), srcPoolName, projectName, vol, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		inst, err := instance.Load(d.State(), dbInst, project)
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
	if (req.Pool == "" || req.Pool == srcPoolName) && (projectName == targetProjectName) {
		return storagePoolVolumeTypePostRename(d, r, srcPoolName, projectName, vol, req)
	}

	// Otherwise this is a move request.
	return storagePoolVolumeTypePostMove(d, r, srcPoolName, projectName, targetProjectName, vol, req)
}

// storagePoolVolumeTypePostMigration handles volume migration type POST requests.
func storagePoolVolumeTypePostMigration(state *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, volumeName string, req api.StorageVolumePost) response.Response {
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

		op, err := operations.OperationCreate(state, requestProjectName, operations.OperationClassTask, operationtype.VolumeMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode
	op, err := operations.OperationCreate(state, requestProjectName, operations.OperationClassWebsocket, operationtype.VolumeMigrate, resources, ws.Metadata(), run, nil, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// storagePoolVolumeTypePostRename handles volume rename type POST requests.
func storagePoolVolumeTypePostRename(d *Daemon, r *http.Request, poolName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.LoadByName(d.State(), poolName)
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

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	err = pool.RenameCustomVolume(projectName, vol.Name, req.Name, op)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()

	u := api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "volumes", db.StoragePoolVolumeTypeNameCustom, req.Name).Project(projectName)

	return response.SyncResponseLocation(true, nil, u.String())
}

// storagePoolVolumeTypePostMove handles volume move type POST requests.
func storagePoolVolumeTypePostMove(d *Daemon, r *http.Request, poolName string, requestProjectName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	newPool, err := storagePools.LoadByName(d.State(), req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	run := func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		// Update devices using the volume in instances and profiles.
		err = storagePoolVolumeUpdateUsers(d, requestProjectName, pool.Name(), vol, newPool.Name(), &newVol)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = storagePoolVolumeUpdateUsers(d, projectName, newPool.Name(), &newVol, pool.Name(), vol) })

		// Provide empty description and nil config to instruct CreateCustomVolumeFromCopy to copy it
		// from source volume.
		err = newPool.CreateCustomVolumeFromCopy(projectName, requestProjectName, newVol.Name, "", nil, pool.Name(), vol.Name, true, op)
		if err != nil {
			return err
		}

		err = pool.DeleteCustomVolume(requestProjectName, vol.Name, op)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(d.State(), requestProjectName, operations.OperationClassTask, operationtype.VolumeMove, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// storageGetVolumeNameFromURL retrieves the volume name from the URL name segment.
func storageGetVolumeNameFromURL(r *http.Request) (string, error) {
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return "", err
	}

	fields := strings.Split(volumeName, "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots.
		return fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2]), nil
	} else if len(fields) > 1 {
		return fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1]), nil
	} else if len(fields) > 0 {
		// Handle volume.
		return fields[0], nil
	}

	return "", fmt.Errorf("Invalid storage volume %q", volumeName)
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume} storage storage_pool_volume_type_get
//
// Get the storage volume
//
// Gets a specific storage volume.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: Storage volume
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           $ref: "#/definitions/StorageVolume"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeGet(d *Daemon, r *http.Request) response.Response {
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	requestProjectName := projectParam(r)
	volumeProjectName, err := project.StorageVolumeProject(d.State().DB.Cluster, requestProjectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, volumeProjectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the ID of the storage pool the storage volume is supposed to be attached to.
	poolID, err := d.db.Cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the storage volume.
	var dbVolume *db.StorageVolume
	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(poolID, volumeProjectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), requestProjectName, poolName, dbVolume)
	if err != nil {
		return response.SmartError(err)
	}

	dbVolume.UsedBy = project.FilterUsedBy(r, volumeUsedBy)

	etag := []any{volumeName, dbVolume.Type, dbVolume.Config}

	return response.SyncResponseETag(true, dbVolume.StorageVolume, etag)
}

// swagger:operation PUT /1.0/storage-pools/{name}/volumes/{type}/{volume} storage storage_pool_volume_type_put
//
// Update the storage volume
//
// Updates the entire storage volume configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: storage volume
//     description: Storage volume configuration
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumePut(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, err = project.StorageVolumeProject(d.State().DB.Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	pool, err := storagePools.LoadByName(d.State(), poolName)
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
	_, vol, err := d.db.Cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{volumeName, vol.Type, vol.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Possibly check if project limits are honored.
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return project.AllowVolumeUpdate(tx, projectName, volumeName, req, vol.Config)
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Restore custom volume from snapshot if requested. This should occur first
		// before applying config changes so that changes are applied to the
		// restored volume.
		if req.Restore != "" {
			err = pool.RestoreCustomVolume(projectName, vol.Name, req.Restore, op)
			if err != nil {
				return response.SmartError(err)
			}
		}

		// Handle custom volume update requests.
		// Only apply changes during a snapshot restore if a non-nil config is supplied to avoid clearing
		// the volume's config if only restoring snapshot.
		if req.Config != nil || req.Restore == "" {
			err = pool.UpdateCustomVolume(projectName, vol.Name, req.Description, req.Config, op)
			if err != nil {
				return response.SmartError(err)
			}
		}
	} else if volumeType == db.StoragePoolVolumeTypeContainer || volumeType == db.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, vol.Name)
		if err != nil {
			return response.SmartError(err)
		}

		// Handle instance volume update requests.
		err = pool.UpdateInstance(inst, req.Description, req.Config, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else if volumeType == db.StoragePoolVolumeTypeImage {
		// Handle image update requests.
		err = pool.UpdateImage(vol.Name, req.Description, req.Config, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		return response.SmartError(fmt.Errorf("Invalid volume type"))
	}

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/storage-pools/{name}/volumes/{type}/{volume} storage storage_pool_volume_type_patch
//
// Partially update the storage volume
//
// Updates a subset of the storage volume configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: storage volume
//     description: Storage volume configuration
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumePatch(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is custom.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().DB.Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(d.State(), poolName)
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
	_, vol, err := d.db.Cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []any{volumeName, vol.Type, vol.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
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

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	err = pool.UpdateCustomVolume(projectName, vol.Name, req.Description, req.Config, op)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{name}/volumes/{type}/{volume} storage storage_pool_volume_type_delete
//
// Delete the storage volume
//
// Removes the storage volume.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeDelete(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %q", volumeName))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	requestProjectName := projectParam(r)
	volumeProjectName, err := project.StorageVolumeProject(d.State().DB.Cluster, requestProjectName, volumeType)
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

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, volumeProjectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	if volumeType != db.StoragePoolVolumeTypeCustom && volumeType != db.StoragePoolVolumeTypeImage {
		return response.BadRequest(fmt.Errorf("Storage volumes of type %q cannot be deleted with the storage API", volumeTypeName))
	}

	// Get the storage pool the storage volume is supposed to be attached to.
	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the storage volume.
	var dbVolume *db.StorageVolume
	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(pool.ID(), volumeProjectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), requestProjectName, poolName, dbVolume)
	if err != nil {
		return response.SmartError(err)
	}

	// isImageURL checks whether the provided usedByURL represents an image resource for the fingerprint.
	isImageURL := func(usedByURL string, fingerprint string) bool {
		usedBy, _ := url.Parse(usedByURL)
		if usedBy == nil {
			return false
		}

		img := api.NewURL().Path(version.APIVersion, "images", fingerprint)
		return usedBy.Path == img.URL.Path
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 || volumeType != db.StoragePoolVolumeTypeImage || !isImageURL(volumeUsedBy[0], dbVolume.Name) {
			return response.BadRequest(fmt.Errorf("The storage volume is still in use"))
		}
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	switch volumeType {
	case db.StoragePoolVolumeTypeCustom:
		err = pool.DeleteCustomVolume(volumeProjectName, volumeName, op)
	case db.StoragePoolVolumeTypeImage:
		err = pool.DeleteImage(volumeName, op)
	default:
		return response.BadRequest(fmt.Errorf(`Storage volumes of type %q cannot be deleted with the storage API`, volumeTypeName))
	}

	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func createStoragePoolVolumeFromBackup(d *Daemon, r *http.Request, requestProjectName string, projectName string, data io.Reader, pool string, volName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Create temporary file to store uploaded backup data.
	backupFile, err := ioutil.TempFile(shared.VarPath("backups"), fmt.Sprintf("%s_", backup.WorkingDirPrefix))
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = os.Remove(backupFile.Name()) }()
	revert.Add(func() { _ = backupFile.Close() })

	// Stream uploaded backup data into temporary file.
	_, err = io.Copy(backupFile, data)
	if err != nil {
		return response.InternalError(err)
	}

	// Detect squashfs compression and convert to tarball.
	_, err = backupFile.Seek(0, 0)
	if err != nil {
		return response.InternalError(err)
	}

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

		defer func() { _ = os.Remove(tarFile.Name()) }()

		// Decompress to tarFile temporary file.
		err = archive.ExtractWithFds(decomArgs[0], decomArgs[1:], nil, nil, d.State().OS, tarFile)
		if err != nil {
			return response.InternalError(err)
		}

		// We don't need the original squashfs file anymore.
		_ = backupFile.Close()
		_ = os.Remove(backupFile.Name())

		// Replace the backup file handle with the handle to the tar file.
		backupFile = tarFile
	}

	// Parse the backup information.
	_, err = backupFile.Seek(0, 0)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Debug("Reading backup file info")
	bInfo, err := backup.GetInfo(backupFile, d.State().OS, backupFile.Name())
	if err != nil {
		return response.BadRequest(err)
	}

	bInfo.Project = projectName

	// Override pool.
	if pool != "" {
		bInfo.Pool = pool
	}

	// Override volume name.
	if volName != "" {
		bInfo.Name = volName
	}

	logger.Debug("Backup file info loaded", logger.Ctx{
		"type":      bInfo.Type,
		"name":      bInfo.Name,
		"project":   bInfo.Project,
		"backend":   bInfo.Backend,
		"pool":      bInfo.Pool,
		"optimized": *bInfo.OptimizedStorage,
		"snapshots": bInfo.Snapshots,
	})

	// Check storage pool exists.
	_, _, _, err = d.State().DB.Cluster.GetStoragePoolInAnyState(bInfo.Pool)
	if response.IsNotFoundError(err) {
		// The storage pool doesn't exist. If backup is in binary format (so we cannot alter
		// the backup.yaml) or the pool has been specified directly from the user restoring
		// the backup then we cannot proceed so return an error.
		if *bInfo.OptimizedStorage || pool != "" {
			return response.InternalError(fmt.Errorf("Storage pool not found: %w", err))
		}

		// Otherwise try and restore to the project's default profile pool.
		_, profile, err := d.State().DB.Cluster.GetProfile(bInfo.Project, "default")
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get default profile: %w", err))
		}

		_, v, err := shared.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get root disk device: %w", err))
		}

		// Use the default-profile's root pool.
		bInfo.Pool = v["pool"]
	} else if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer func() { _ = backupFile.Close() }()
		defer runRevert.Fail()

		pool, err := storagePools.LoadByName(d.State(), bInfo.Pool)
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
			return fmt.Errorf("Create custom volume from backup: %w", err)
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{bInfo.Name}

	op, err := operations.OperationCreate(d.State(), requestProjectName, operations.OperationClassTask, operationtype.CustomVolumeBackupRestore, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}
