package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/lxd/archive"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/filter"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

var storageVolumesCmd = APIEndpoint{
	Path: "storage-volumes",

	Get: APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectResourceList},
}

var storageVolumesTypeCmd = APIEndpoint{
	Path: "storage-volumes/{type}",

	Get: APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectResourceList},
}

var storagePoolVolumesCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var storagePoolVolumeTypeCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeDelete, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: storagePoolVolumeGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanView)},
	Patch:  APIEndpointAction{Handler: storagePoolVolumePatch, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: storagePoolVolumePost, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: storagePoolVolumePut, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanEdit)},
}

// storagePoolVolumeTypeAccessHandler returns an access handler which checks the given entitlement on a storage volume.
func storagePoolVolumeTypeAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		err := addStoragePoolVolumeDetailsToRequestContext(s, r)
		if err != nil {
			return response.SmartError(err)
		}

		details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
		if err != nil {
			return response.SmartError(err)
		}

		var target string

		// Regardless of whether the caller specified a target parameter, we do not add it to the authorization check if
		// the storage pool is remote. This is because the volume in the database has a NULL `node_id`, so the URL uniquely
		// identifies the volume without the target parameter.
		if !details.pool.Driver().Info().Remote {
			// If the storage pool is local, we need to add a target parameter to the authorization check URL for the
			// auth subsystem to consider it unique.

			// If the target parameter was specified, use that.
			target = request.QueryParam(r, "target")

			if target == "" {
				// Otherwise, check if the volume is located on another member.
				if details.forwardingNodeInfo != nil {
					// Use the name of the forwarding member as the location of the volume.
					target = details.forwardingNodeInfo.Name
				} else {
					// If we're not forwarding the request, use the name of this member as the location of the volume.
					target = s.ServerName
				}
			}
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.StorageVolumeURL(request.ProjectParam(r), target, details.pool.Name(), details.volumeTypeName, details.volumeName), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

// swagger:operation GET /1.0/storage-volumes storage storage_volumes_get
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Indicates whether volumes from all projects should be returned
//      type: bool
//      example: true
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/storage-pools/local/volumes/container/a1",
//                "/1.0/storage-pools/local/volumes/container/a2",
//                "/1.0/storage-pools/local/volumes/custom/backups",
//                "/1.0/storage-pools/local/volumes/custom/images"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-volumes?recursion=1 storage storage_pool_volumes_get_recursion1
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (structs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Indicates whether volumes from all projects should be returned
//      type: bool
//      example: true
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of storage volumes
//            items:
//              $ref: "#/definitions/StorageVolume"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-volumes/{type} storage storage_pool_volumes_type_get
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (URLs) (type specific endpoint).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Indicates whether volumes from all projects should be returned
//      type: bool
//      example: true
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/storage-pools/local/volumes/custom/backups",
//                "/1.0/storage-pools/local/volumes/custom/images"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-volumes/{type}?recursion=1 storage storage_pool_volumes_type_get_recursion1
//
//	Get the storage volumes
//
//	Returns a list of storage volumes (structs) (type specific endpoint).
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
//	    name: all-projects
//	    description: Indicates whether volumes from all projects should be returned
//	    type: bool
//	    example: true
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: API endpoints
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
//	          type: array
//	          description: List of storage volumes
//	          items:
//	            $ref: "#/definitions/StorageVolume"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes storage storage_pool_volumes_get
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Indicates whether volumes from all projects should be returned
//      type: bool
//      example: true
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/storage-pools/local/volumes/container/a1",
//                "/1.0/storage-pools/local/volumes/container/a2",
//                "/1.0/storage-pools/local/volumes/custom/backups",
//                "/1.0/storage-pools/local/volumes/custom/images"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes?recursion=1 storage storage_pool_volumes_get_recursion1
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (structs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Indicates whether volumes from all projects should be returned
//      type: bool
//      example: true
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of storage volumes
//            items:
//              $ref: "#/definitions/StorageVolume"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type} storage storage_pool_volumes_type_get
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (URLs) (type specific endpoint).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Indicates whether volumes from all projects should be returned
//      type: bool
//      example: true
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/storage-pools/local/volumes/custom/backups",
//                "/1.0/storage-pools/local/volumes/custom/images"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}?recursion=1 storage storage_pool_volumes_type_get_recursion1
//
//	Get the storage volumes
//
//	Returns a list of storage volumes (structs) (type specific endpoint).
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
//	    name: all-projects
//	    description: Indicates whether volumes from all projects should be returned
//	    type: bool
//	    example: true
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: API endpoints
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
//	          type: array
//	          description: List of storage volumes
//	          items:
//	            $ref: "#/definitions/StorageVolume"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check if current route is in /1.0/storage-volumes
	allPools := poolName == ""

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
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid filter: %w", err))
	}

	var poolID int64

	if !allPools {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			poolID, err = tx.GetStoragePoolID(ctx, poolName)

			return err
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Detect project mode.
	requestProjectName := request.QueryParam(r, "project")
	allProjects := shared.IsTrue(request.QueryParam(r, "all-projects"))

	if allProjects && requestProjectName != "" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects"))
	} else if !allProjects && requestProjectName == "" {
		requestProjectName = api.ProjectDefaultName
	}

	var dbVolumes []*db.StorageVolume
	var projectImages []string
	var customVolProjectName string

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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
			customVolProjectName = project.StorageVolumeProjectFromRecord(p, cluster.StoragePoolVolumeTypeCustom)

			projectImages, err = tx.GetImagesFingerprints(ctx, requestProjectName, false)
			if err != nil {
				return err
			}
		}

		filters := make([]db.StorageVolumeFilter, 0)

		for i := range supportedVolumeTypes {
			supportedVolType := supportedVolumeTypes[i] // Local variable for use as pointer below.

			if volumeTypeName != "" && supportedVolType != volumeType {
				continue // Only include the requested type if specified.
			}

			switch supportedVolType {
			case cluster.StoragePoolVolumeTypeCustom:
				volTypeCustom := cluster.StoragePoolVolumeTypeCustom
				filter := db.StorageVolumeFilter{
					Type: &volTypeCustom,
				}

				if !allProjects {
					filter.Project = &customVolProjectName
				}

				filters = append(filters, filter)
			case cluster.StoragePoolVolumeTypeImage:
				// Image volumes are effectively a cache and are always linked to default project.
				// We filter the ones relevant to requested project below after the query has run.
				volTypeImage := cluster.StoragePoolVolumeTypeImage
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

		if allPools {
			dbVolumes, err = tx.GetStorageVolumes(ctx, memberSpecific, filters...)
			if err != nil {
				return fmt.Errorf("Failed loading storage volumes: %w", err)
			}

			return err
		}

		for i := range filters {
			filters[i].PoolID = &poolID
		}

		dbVolumes, err = tx.GetStorageVolumes(ctx, memberSpecific, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage volumes: %w", err)
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Pre-fill UsedBy if using filtering.
	if clauses != nil && len(clauses.Clauses) > 0 {
		for i, vol := range dbVolumes {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, vol)
			if err != nil {
				return response.InternalError(err)
			}

			dbVolumes[i].UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)
		}
	}

	// Filter the results.
	dbVolumes, err = filterVolumes(dbVolumes, clauses, allProjects, projectImages)
	if err != nil {
		return response.SmartError(err)
	}

	// Sort by type then volume name.
	sort.SliceStable(dbVolumes, func(i, j int) bool {
		volA := dbVolumes[i]
		volB := dbVolumes[j]

		if volA.Type != volB.Type {
			return dbVolumes[i].Type < dbVolumes[j].Type
		}

		return volA.Name < volB.Name
	})

	// If we're requesting for just one project, set the effective project name of volumes in this project.
	if !allProjects {
		request.SetCtxValue(r, request.CtxEffectiveProjectName, customVolProjectName)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeStorageVolume)
	if err != nil {
		return response.SmartError(err)
	}

	// The auth.PermissionChecker expects the url to contain the request project (not the effective project).
	// So when getting networks in a single project, ensure we use the request project name.
	authCheckProject := func(dbProject string) string {
		if !allProjects {
			return requestProjectName
		}

		return dbProject
	}

	if util.IsRecursionRequest(r) {
		volumes := make([]*api.StorageVolume, 0, len(dbVolumes))
		for _, dbVol := range dbVolumes {
			vol := &dbVol.StorageVolume

			volumeName, _, _ := api.GetParentAndSnapshotName(vol.Name)
			if !userHasPermission(entity.StorageVolumeURL(authCheckProject(vol.Project), vol.Location, dbVol.Pool, dbVol.Type, volumeName)) {
				continue
			}

			// Fill in UsedBy if we haven't previously done so.
			if clauses == nil || len(clauses.Clauses) == 0 {
				volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, dbVol)
				if err != nil {
					return response.InternalError(err)
				}

				vol.UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)
			}

			volumes = append(volumes, vol)
		}

		return response.SyncResponse(true, volumes)
	}

	urls := make([]string, 0, len(dbVolumes))
	for _, dbVol := range dbVolumes {
		volumeName, _, _ := api.GetParentAndSnapshotName(dbVol.Name)

		if !userHasPermission(entity.StorageVolumeURL(authCheckProject(dbVol.Project), dbVol.Location, dbVol.Pool, dbVol.Type, volumeName)) {
			continue
		}

		urls = append(urls, dbVol.StorageVolume.URL(version.APIVersion).String())
	}

	return response.SyncResponse(true, urls)
}

// filterVolumes returns a filtered list of volumes that match the given clauses.
func filterVolumes(volumes []*db.StorageVolume, clauses *filter.ClauseSet, allProjects bool, filterProjectImages []string) ([]*db.StorageVolume, error) {
	// FilterStorageVolume is for filtering purpose only.
	// It allows to filter snapshots by using default filter mechanism.
	type FilterStorageVolume struct {
		api.StorageVolume `yaml:",inline"`
		Snapshot          string `yaml:"snapshot"`
	}

	filtered := []*db.StorageVolume{}
	for _, volume := range volumes {
		// Filter out image volumes that are not used by this project.
		if volume.Type == cluster.StoragePoolVolumeTypeNameImage && !allProjects && !shared.ValueInSlice(volume.Name, filterProjectImages) {
			continue
		}

		tmpVolume := FilterStorageVolume{
			StorageVolume: volume.StorageVolume,
			Snapshot:      strconv.FormatBool(strings.Contains(volume.Name, shared.SnapshotDelimiter)),
		}

		match, err := filter.Match(tmpVolume, *clauses)
		if err != nil {
			return nil, err
		}

		if !match {
			continue
		}

		filtered = append(filtered, volume)
	}

	return filtered, nil
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes storage storage_pool_volumes_post
//
//	Add a storage volume
//
//	Creates a new storage volume.
//	Will return an empty sync response on simple volume creation but an operation on copy or migration.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: volume
//	    description: Storage volume
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type} storage storage_pool_volumes_type_post
//
//	Add a storage volume
//
//	Creates a new storage volume (type specific endpoint).
//	Will return an empty sync response on simple volume creation but an operation on copy or migration.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: volume
//	    description: Storage volume
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	requestProjectName := request.ProjectParam(r)
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, requestProjectName, cluster.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// If we're getting binary content, process separately.
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		if r.Header.Get("X-LXD-type") == "iso" {
			return createStoragePoolVolumeFromISO(s, r, requestProjectName, projectName, r.Body, poolName, r.Header.Get("X-LXD-name"))
		}

		return createStoragePoolVolumeFromBackup(s, r, requestProjectName, projectName, r.Body, poolName, r.Header.Get("X-LXD-name"))
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check new volume name is valid.
	err = storagePools.ValidVolumeName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Backward compatibility.
	if req.ContentType == "" {
		req.ContentType = cluster.StoragePoolVolumeContentTypeNameFS
	}

	_, err = storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.BadRequest(err)
	}

	// Handle being called through the typed URL.
	_, ok := mux.Vars(r)["type"]
	if ok {
		req.Type, err = url.PathUnescape(mux.Vars(r)["type"])
		if err != nil {
			return response.SmartError(err)
		}
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != cluster.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Currently not allowed to create storage volumes of type %q", req.Type))
	}

	var poolID int64
	var dbVolume *db.StorageVolume

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		poolID, err = tx.GetStoragePoolID(ctx, poolName)
		if err != nil {
			return err
		}

		// Check if destination volume exists.
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, cluster.StoragePoolVolumeTypeCustom, req.Name, true)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		err = limits.AllowVolumeCreation(s.GlobalConfig, tx, projectName, req)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	} else if dbVolume != nil && !req.Source.Refresh {
		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	target := request.QueryParam(r, "target")

	// Check if we need to switch to migration
	serverName := s.ServerName
	var nodeAddress string

	if s.ServerClustered && target != "" && (req.Source.Location != "" && serverName != req.Source.Location) {
		err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			nodeInfo, err := tx.GetNodeByName(ctx, req.Source.Location)
			if err != nil {
				return err
			}

			nodeAddress = nodeInfo.Address

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		if nodeAddress == "" {
			return response.BadRequest(fmt.Errorf("The source is currently offline"))
		}

		return clusterCopyCustomVolumeInternal(s, r, nodeAddress, projectName, poolName, &req)
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(s, r, requestProjectName, projectName, poolName, &req)
	case "copy":
		if dbVolume != nil {
			return doCustomVolumeRefresh(s, r, requestProjectName, projectName, poolName, &req)
		}

		return doVolumeCreateOrCopy(s, r, requestProjectName, projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(s, r, requestProjectName, projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func clusterCopyCustomVolumeInternal(s *state.State, r *http.Request, sourceAddress string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	websockets := map[string]string{}

	client, err := lxdCluster.Connect(sourceAddress, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	client = client.UseProject(req.Source.Project)

	pullReq := api.StorageVolumePost{
		Name:       req.Source.Name,
		Pool:       req.Source.Pool,
		Migration:  true,
		VolumeOnly: req.Source.VolumeOnly,
		Project:    req.Source.Project,
		Source: api.StorageVolumeSource{
			Location: req.Source.Location,
		},
	}

	op, err := client.MigrateStoragePoolVolume(req.Source.Pool, pullReq)
	if err != nil {
		return response.SmartError(err)
	}

	opAPI := op.Get()

	for k, v := range opAPI.Metadata {
		ws, ok := v.(string)
		if !ok {
			continue
		}

		websockets[k] = ws
	}

	// Reset the source for a migration
	req.Source.Type = "migration"
	req.Source.Certificate = string(s.Endpoints.NetworkCert().PublicKey())
	req.Source.Mode = "pull"
	req.Source.Operation = fmt.Sprintf("https://%s/%s/operations/%s", sourceAddress, version.APIVersion, opAPI.ID)
	req.Source.Websockets = websockets
	req.Source.Project = ""

	return doVolumeMigration(s, r, req.Source.Project, projectName, poolName, req)
}

func doCustomVolumeRefresh(s *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	var srcProjectName string
	if req.Source.Project != "" {
		srcProjectName, err = project.StorageVolumeProject(s.DB.Cluster, req.Source.Project, cluster.StoragePoolVolumeTypeCustom)
		if err != nil {
			return response.SmartError(err)
		}
	}

	run := func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		if req.Source.Name == "" {
			return fmt.Errorf("No source volume name supplied")
		}

		err = pool.RefreshCustomVolume(projectName, srcProjectName, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, !req.Source.VolumeOnly, op)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func doVolumeCreateOrCopy(s *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	var srcProjectName string
	if req.Source.Project != "" {
		srcProjectName, err = project.StorageVolumeProject(s.DB.Cluster, req.Source.Project, cluster.StoragePoolVolumeTypeCustom)
		if err != nil {
			return response.SmartError(err)
		}
	}

	volumeDBContentType, err := storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeDBContentTypeToContentType(volumeDBContentType)
	if err != nil {
		return response.SmartError(err)
	}

	run := func(op *operations.Operation) error {
		if req.Source.Name == "" {
			// Use an empty operation for this sync response to pass the requestor
			op := &operations.Operation{}
			op.SetRequestor(r)
			return pool.CreateCustomVolume(projectName, req.Name, req.Description, req.Config, contentType, op)
		}

		return pool.CreateCustomVolumeFromCopy(projectName, srcProjectName, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, !req.Source.VolumeOnly, op)
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
	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func doVolumeMigration(s *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
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

	config, err := shared.GetTLSConfig(cert)
	if err != nil {
		return response.InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	// Initialise migrationArgs, don't set the Storage property yet, this is done in DoStorage,
	// to avoid this function relying on the legacy storage layer.
	migrationArgs := migrationSinkArgs{
		url: req.Source.Operation,
		dialer: &websocket.Dialer{
			TLSClientConfig:  config,
			NetDialContext:   shared.RFC3493Dialer,
			HandshakeTimeout: time.Second * 5,
		},
		secrets:    req.Source.Websockets,
		push:       push,
		volumeOnly: req.Source.VolumeOnly,
		refresh:    req.Source.Refresh,
	}

	sink, err := newStorageMigrationSink(&migrationArgs)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", req.Name)}

	run := func(op *operations.Operation) error {
		// And finally run the migration.
		err = sink.DoStorage(s, projectName, poolName, req, op)
		if err != nil {
			logger.Error("Error during migration sink", logger.Ctx{"err": err})
			return fmt.Errorf("Error transferring storage volume: %s", err)
		}

		return nil
	}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(s, requestProjectName, operations.OperationClassWebsocket, operationtype.VolumeCreate, resources, sink.Metadata(), run, nil, sink.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_post
//
//	Rename or move/migrate a storage volume
//
//	Renames, moves a storage volume between pools or migrates an instance to another server.
//
//	The returned operation metadata will vary based on what's requested.
//	For rename or move within the same server, this is a simple background operation with progress data.
//	For migration, in the push case, this will similarly be a background
//	operation with progress data, for the pull case, it will be a websocket
//	operation with a number of secrets to be passed to the target server.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: migration
//	    description: Migration request
//	    schema:
//	      $ref: "#/definitions/StorageVolumePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(details.volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	req := api.StorageVolumePost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check new volume name is valid.
	err = storagePools.ValidVolumeName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if details.volumeTypeName != cluster.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Renaming storage volumes of type %q is not allowed", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	targetProjectName := effectiveProjectName
	if req.Project != "" {
		targetProjectName, err = project.StorageVolumeProject(s.DB.Cluster, req.Project, cluster.StoragePoolVolumeTypeCustom)
		if err != nil {
			return response.SmartError(err)
		}

		// Check whether the effective storage project differs from the requested target project.
		// If they do it means that the requested target project doesn't have features.storage.volumes
		// and this means that the volume would effectively be moved into the default project, and so we
		// require the user explicitly indicates this by targeting it directly.
		if targetProjectName != req.Project {
			return response.BadRequest(fmt.Errorf("Target project does not have features.storage.volumes enabled"))
		}

		if effectiveProjectName == targetProjectName {
			return response.BadRequest(fmt.Errorf("Project and target project are the same"))
		}

		// Check if user has permission to copy/move the volume into the effective project corresponding to the target.
		err := s.Authorizer.CheckPermission(r.Context(), entity.ProjectURL(targetProjectName), auth.EntitlementCanCreateStorageVolumes)
		if err != nil {
			return response.SmartError(err)
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

	target := request.QueryParam(r, "target")

	// Check if clustered.
	if s.ServerClustered && target != "" && req.Source.Location != "" && req.Migration {
		var sourceNodeOffline bool

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Load source node.
			nodeInfo, err := tx.GetNodeByName(ctx, req.Source.Location)
			if err != nil {
				return err
			}

			sourceAddress := nodeInfo.Address

			if sourceAddress == "" {
				// Local node.
				sourceNodeOffline = false
				return nil
			}

			sourceMemberInfo, err := tx.GetNodeByAddress(ctx, sourceAddress)
			if err != nil {
				return fmt.Errorf("Failed to get source member for %q: %w", sourceAddress, err)
			}

			sourceNodeOffline = sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold())

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		var targetProject *api.Project
		var targetMemberInfo *db.NodeInfo

		if sourceNodeOffline {
			resp := forwardedResponseIfTargetIsRemote(s, r)
			if resp != nil {
				return resp
			}

			if details.pool.Driver().Info().Name == "ceph" {
				var dbVolume *db.StorageVolume
				var volumeNotFound bool
				var targetIsSet bool

				err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
					dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, cluster.StoragePoolVolumeTypeCustom, details.volumeName, true)
					if err != nil {
						// Check if the user provided an incorrect target query parameter and return a helpful error message.
						_, volumeNotFound = api.StatusErrorMatch(err, http.StatusNotFound)
						targetIsSet = r.URL.Query().Get("target") != ""

						return err
					}

					return nil
				})
				if err != nil {
					if s.ServerClustered && targetIsSet && volumeNotFound {
						return response.NotFound(fmt.Errorf("Storage volume not found on this cluster member"))
					}

					return response.SmartError(err)
				}

				req := api.StorageVolumePost{
					Name: req.Name,
				}

				return storagePoolVolumeTypePostRename(s, r, details.pool.Name(), effectiveProjectName, &dbVolume.StorageVolume, req)
			}
		} else {
			resp := forwardedResponseToNode(s, r, req.Source.Location)
			if resp != nil {
				return resp
			}
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := cluster.GetProject(ctx, tx.Tx(), effectiveProjectName)
			if err != nil {
				return err
			}

			targetProject, err = p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			allMembers, err := tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			targetMemberInfo, _, err = limits.CheckTarget(ctx, s.Authorizer, r, tx, targetProject, target, allMembers)
			if err != nil {
				return err
			}

			if targetMemberInfo == nil {
				return fmt.Errorf("Failed checking cluster member %q", target)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		if targetMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
			return response.BadRequest(fmt.Errorf("Target cluster member is offline"))
		}

		run := func(op *operations.Operation) error {
			return migrateStorageVolume(s, r, details.volumeName, details.pool.Name(), targetMemberInfo.Name, targetProjectName, req, op)
		}

		resources := map[string][]api.URL{}
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", "custom", details.volumeName)}

		op, err := operations.OperationCreate(s, effectiveProjectName, operations.OperationClassTask, operationtype.VolumeMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// If source is set, we know the source and the target, and therefore don't need this function to figure out where to forward the request to.
	if req.Source.Location == "" {
		resp := forwardedResponseIfVolumeIsRemote(s, r)
		if resp != nil {
			return resp
		}
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		return storagePoolVolumeTypePostMigration(s, r, requestProjectName, effectiveProjectName, details.pool.Name(), details.volumeName, req)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	var targetPoolID int64
	var targetPoolName string

	if req.Pool != "" {
		targetPoolName = req.Pool
	} else {
		targetPoolName = details.pool.Name()
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		targetPoolID, err = tx.GetStoragePoolID(ctx, targetPoolName)

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = tx.GetStoragePoolNodeVolumeID(ctx, targetProjectName, req.Name, details.volumeType, targetPoolID)

		return err
	})
	if !response.IsNotFoundError(err) {
		if err != nil {
			return response.InternalError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	// Check if the daemon itself is using it.
	used, err := storagePools.VolumeUsedByDaemon(s, details.pool.Name(), details.volumeName)
	if err != nil {
		return response.SmartError(err)
	}

	if used {
		return response.SmartError(fmt.Errorf("Volume is used by LXD itself and cannot be renamed"))
	}

	var dbVolume *db.StorageVolume
	var volumeNotFound bool
	var targetIsSet bool

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		if err != nil {
			// Check if the user provided an incorrect target query parameter and return a helpful error message.
			_, volumeNotFound = api.StatusErrorMatch(err, http.StatusNotFound)
			targetIsSet = r.URL.Query().Get("target") != ""

			return err
		}

		return nil
	})
	if err != nil {
		if s.ServerClustered && targetIsSet && volumeNotFound {
			return response.NotFound(fmt.Errorf("Storage volume not found on this cluster member"))
		}

		return response.SmartError(err)
	}

	// Check if a running instance is using it.
	err = storagePools.VolumeUsedByInstanceDevices(s, details.pool.Name(), effectiveProjectName, &dbVolume.StorageVolume, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		inst, err := instance.Load(s, dbInst, project)
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
	if (req.Pool == "" || req.Pool == details.pool.Name()) && (effectiveProjectName == targetProjectName) {
		return storagePoolVolumeTypePostRename(s, r, details.pool.Name(), effectiveProjectName, &dbVolume.StorageVolume, req)
	}

	// Otherwise this is a move request.
	return storagePoolVolumeTypePostMove(s, r, details.pool.Name(), effectiveProjectName, targetProjectName, &dbVolume.StorageVolume, req)
}

func migrateStorageVolume(s *state.State, r *http.Request, sourceVolumeName string, sourcePoolName string, targetNode string, projectName string, req api.StorageVolumePost, op *operations.Operation) error {
	if targetNode == req.Source.Location {
		return fmt.Errorf("Target must be different than storage volumes' current location")
	}

	var err error
	var srcMember, newMember db.NodeInfo

	// If the source member is online then get its address so we can connect to it and see if the
	// instance is running later.
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		srcMember, err = tx.GetNodeByName(ctx, req.Source.Location)
		if err != nil {
			return fmt.Errorf("Failed getting current cluster member of storage volume %q", req.Source.Name)
		}

		newMember, err = tx.GetNodeByName(ctx, targetNode)
		if err != nil {
			return fmt.Errorf("Failed loading new cluster member for storage volume: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	srcPool, err := storagePools.LoadByName(s, sourcePoolName)
	if err != nil {
		return fmt.Errorf("Failed loading storage volume storage pool: %w", err)
	}

	f, err := storageVolumePostClusteringMigrate(s, r, srcPool, projectName, sourceVolumeName, req.Pool, req.Project, req.Name, srcMember, newMember, req.VolumeOnly)
	if err != nil {
		return err
	}

	return f(op)
}

func storageVolumePostClusteringMigrate(s *state.State, r *http.Request, srcPool storagePools.Pool, srcProjectName string, srcVolumeName string, newPoolName string, newProjectName string, newVolumeName string, srcMember db.NodeInfo, newMember db.NodeInfo, volumeOnly bool) (func(op *operations.Operation) error, error) {
	srcMemberOffline := srcMember.IsOffline(s.GlobalConfig.OfflineThreshold())

	// Make sure that the source member is online if we end up being called from another member after a
	// redirection due to the source member being offline.
	if srcMemberOffline {
		return nil, fmt.Errorf("The cluster member hosting the storage volume is offline")
	}

	run := func(op *operations.Operation) error {
		if newVolumeName == "" {
			newVolumeName = srcVolumeName
		}

		networkCert := s.Endpoints.NetworkCert()

		// Connect to the destination member, i.e. the member to migrate the custom volume to.
		// Use the notify argument to indicate to the destination that we are moving a custom volume between
		// cluster members.
		dest, err := lxdCluster.Connect(newMember.Address, networkCert, s.ServerCert(), r, true)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", newMember.Address, err)
		}

		dest = dest.UseTarget(newMember.Name).UseProject(srcProjectName)

		resources := map[string][]api.URL{}
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", srcPool.Name(), "volumes", "custom", srcVolumeName)}

		srcMigration, err := newStorageMigrationSource(volumeOnly, nil)
		if err != nil {
			return fmt.Errorf("Failed setting up storage volume migration on source: %w", err)
		}

		run := func(op *operations.Operation) error {
			err := srcMigration.DoStorage(s, srcProjectName, srcPool.Name(), srcVolumeName, op)
			if err != nil {
				return err
			}

			err = srcPool.DeleteCustomVolume(srcProjectName, srcVolumeName, op)
			if err != nil {
				return err
			}

			return nil
		}

		cancel := func(op *operations.Operation) error {
			srcMigration.disconnect()
			return nil
		}

		srcOp, err := operations.OperationCreate(s, srcProjectName, operations.OperationClassWebsocket, operationtype.VolumeMigrate, resources, srcMigration.Metadata(), run, cancel, srcMigration.Connect, r)
		if err != nil {
			return err
		}

		err = srcOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation: %w", err)
		}

		sourceSecrets := make(map[string]string, len(srcMigration.conns))
		for connName, conn := range srcMigration.conns {
			sourceSecrets[connName] = conn.Secret()
		}

		// Request pull mode migration on destination.
		err = dest.CreateStoragePoolVolume(newPoolName, api.StorageVolumesPost{
			Name: newVolumeName,
			Type: "custom",
			Source: api.StorageVolumeSource{
				Type:        "migration",
				Mode:        "pull",
				Operation:   fmt.Sprintf("https://%s%s", srcMember.Address, srcOp.URL()),
				Websockets:  sourceSecrets,
				Certificate: string(networkCert.PublicKey()),
				Name:        newVolumeName,
				Pool:        newPoolName,
				Project:     newProjectName,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination: %w", err)
		}

		return nil
	}

	return run, nil
}

// storagePoolVolumeTypePostMigration handles volume migration type POST requests.
func storagePoolVolumeTypePostMigration(state *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, volumeName string, req api.StorageVolumePost) response.Response {
	ws, err := newStorageMigrationSource(req.VolumeOnly, req.Target)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]api.URL{}
	srcVolParentName, srcVolSnapName, srcIsSnapshot := api.GetParentAndSnapshotName(volumeName)
	if srcIsSnapshot {
		resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", srcVolParentName, "snapshots", srcVolSnapName)}
	} else {
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", volumeName)}
	}

	run := func(op *operations.Operation) error {
		return ws.DoStorage(state, projectName, poolName, volumeName, op)
	}

	if req.Target != nil {
		// Push mode.
		op, err := operations.OperationCreate(state, requestProjectName, operations.OperationClassTask, operationtype.VolumeMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode.
	op, err := operations.OperationCreate(state, requestProjectName, operations.OperationClassWebsocket, operationtype.VolumeMigrate, resources, ws.Metadata(), run, nil, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// storagePoolVolumeTypePostRename handles volume rename type POST requests.
func storagePoolVolumeTypePostRename(s *state.State, r *http.Request, poolName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	revert := revert.New()
	defer revert.Fail()

	// Update devices using the volume in instances and profiles.
	err = storagePoolVolumeUpdateUsers(s, projectName, pool.Name(), vol, pool.Name(), &newVol)
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

	u := api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "volumes", cluster.StoragePoolVolumeTypeNameCustom, req.Name).Project(projectName)

	return response.SyncResponseLocation(true, nil, u.String())
}

// storagePoolVolumeTypePostMove handles volume move type POST requests.
func storagePoolVolumeTypePostMove(s *state.State, r *http.Request, poolName string, requestProjectName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	newPool, err := storagePools.LoadByName(s, req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	run := func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		// Update devices using the volume in instances and profiles.
		err = storagePoolVolumeUpdateUsers(s, requestProjectName, pool.Name(), vol, newPool.Name(), &newVol)
		if err != nil {
			return err
		}

		revert.Add(func() {
			_ = storagePoolVolumeUpdateUsers(s, projectName, newPool.Name(), &newVol, pool.Name(), vol)
		})

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

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeMove, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_get
//
//	Get the storage volume
//
//	Gets a specific storage volume.
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
//	    description: Storage volume
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
//	          $ref: "#/definitions/StorageVolume"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.ValueInSlice(details.volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	var dbVolume *db.StorageVolume

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the storage volume.
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, dbVolume)
	if err != nil {
		return response.SmartError(err)
	}

	dbVolume.UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)

	etag := []any{details.volumeName, dbVolume.Type, dbVolume.Config}

	return response.SyncResponseETag(true, dbVolume.StorageVolume, etag)
}

// swagger:operation PUT /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_put
//
//	Update the storage volume
//
//	Updates the entire storage volume configuration.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: storage volume
//	    description: Storage volume configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.ValueInSlice(details.volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{details.volumeName, dbVolume.Type, dbVolume.Config}

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

	if details.volumeType == cluster.StoragePoolVolumeTypeCustom {
		// Restore custom volume from snapshot if requested. This should occur first
		// before applying config changes so that changes are applied to the
		// restored volume.
		if req.Restore != "" {
			err = details.pool.RestoreCustomVolume(effectiveProjectName, dbVolume.Name, req.Restore, op)
			if err != nil {
				return response.SmartError(err)
			}
		}

		// Handle custom volume update requests.
		// Only apply changes during a snapshot restore if a non-nil config is supplied to avoid clearing
		// the volume's config if only restoring snapshot.
		if req.Config != nil || req.Restore == "" {
			// Possibly check if project limits are honored.
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				return limits.AllowVolumeUpdate(s.GlobalConfig, tx, effectiveProjectName, details.volumeName, req, dbVolume.Config)
			})
			if err != nil {
				return response.SmartError(err)
			}

			err = details.pool.UpdateCustomVolume(effectiveProjectName, dbVolume.Name, req.Description, req.Config, op)
			if err != nil {
				return response.SmartError(err)
			}
		}
	} else if details.volumeType == cluster.StoragePoolVolumeTypeContainer || details.volumeType == cluster.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(s, effectiveProjectName, dbVolume.Name)
		if err != nil {
			return response.SmartError(err)
		}

		// Handle instance volume update requests.
		err = details.pool.UpdateInstance(inst, req.Description, req.Config, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else if details.volumeType == cluster.StoragePoolVolumeTypeImage {
		// Handle image update requests.
		err = details.pool.UpdateImage(dbVolume.Name, req.Description, req.Config, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		return response.SmartError(fmt.Errorf("Invalid volume type"))
	}

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_patch
//
//	Partially update the storage volume
//
//	Updates a subset of the storage volume configuration.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: storage volume
//	    description: Storage volume configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(details.volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Check that the storage volume type is custom.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []any{details.volumeName, dbVolume.Type, dbVolume.Config}

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
	for k, v := range dbVolume.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	err = details.pool.UpdateCustomVolume(effectiveProjectName, dbVolume.Name, req.Description, req.Config, op)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_delete
//
//	Delete the storage volume
//
//	Removes the storage volume.
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
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(details.volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %q", details.volumeName))
	}

	// Check that the storage volume type is valid.
	if !shared.ValueInSlice(details.volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	if details.volumeType != cluster.StoragePoolVolumeTypeCustom && details.volumeType != cluster.StoragePoolVolumeTypeImage {
		return response.BadRequest(fmt.Errorf("Storage volumes of type %q cannot be deleted with the storage API", details.volumeTypeName))
	}

	// Get the storage volume.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, dbVolume)
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
		if len(volumeUsedBy) != 1 || details.volumeType != cluster.StoragePoolVolumeTypeImage || !isImageURL(volumeUsedBy[0], dbVolume.Name) {
			return response.BadRequest(fmt.Errorf("The storage volume is still in use"))
		}
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	switch details.volumeType {
	case cluster.StoragePoolVolumeTypeCustom:
		err = details.pool.DeleteCustomVolume(effectiveProjectName, details.volumeName, op)
	case cluster.StoragePoolVolumeTypeImage:
		err = details.pool.DeleteImage(details.volumeName, op)
	default:
		return response.BadRequest(fmt.Errorf(`Storage volumes of type %q cannot be deleted with the storage API`, details.volumeTypeName))
	}

	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func createStoragePoolVolumeFromISO(s *state.State, r *http.Request, requestProjectName string, projectName string, data io.Reader, pool string, volName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	if volName == "" {
		return response.BadRequest(fmt.Errorf("Missing volume name"))
	}

	// Create isos directory if needed.
	if !shared.PathExists(shared.VarPath("isos")) {
		err := os.MkdirAll(shared.VarPath("isos"), 0644)
		if err != nil {
			return response.InternalError(err)
		}
	}

	// Create temporary file to store uploaded ISO data.
	isoFile, err := os.CreateTemp(shared.VarPath("isos"), fmt.Sprintf("%s_", "lxd_iso"))
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = os.Remove(isoFile.Name()) }()
	revert.Add(func() { _ = isoFile.Close() })

	// Stream uploaded ISO data into temporary file.
	size, err := io.Copy(isoFile, data)
	if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer func() { _ = isoFile.Close() }()
		defer runRevert.Fail()

		pool, err := storagePools.LoadByName(s, pool)
		if err != nil {
			return err
		}

		// Dump ISO to storage.
		err = pool.CreateCustomVolumeFromISO(projectName, volName, isoFile, size, op)
		if err != nil {
			return fmt.Errorf("Failed creating custom volume from ISO: %w", err)
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", pool, "volumes", "custom", volName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCreate, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}

func createStoragePoolVolumeFromBackup(s *state.State, r *http.Request, requestProjectName string, projectName string, data io.Reader, pool string, volName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Create temporary file to store uploaded backup data.
	backupFile, err := os.CreateTemp(shared.VarPath("backups"), fmt.Sprintf("%s_", backup.WorkingDirPrefix))
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
	_, err = backupFile.Seek(0, io.SeekStart)
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
		tarFile, err := os.CreateTemp(shared.VarPath("backups"), fmt.Sprintf("%s_decompress_", backup.WorkingDirPrefix))
		if err != nil {
			return response.InternalError(err)
		}

		defer func() { _ = os.Remove(tarFile.Name()) }()

		// Decompress to tarFile temporary file.
		err = archive.ExtractWithFds(decomArgs[0], decomArgs[1:], nil, nil, s.OS, tarFile)
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
	_, err = backupFile.Seek(0, io.SeekStart)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Debug("Reading backup file info")
	bInfo, err := backup.GetInfo(backupFile, s.OS, backupFile.Name())
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

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check storage pool exists.
		_, _, _, err = tx.GetStoragePoolInAnyState(ctx, bInfo.Pool)

		return err
	})
	if response.IsNotFoundError(err) {
		// The storage pool doesn't exist. If backup is in binary format (so we cannot alter
		// the backup.yaml) or the pool has been specified directly from the user restoring
		// the backup then we cannot proceed so return an error.
		if *bInfo.OptimizedStorage || pool != "" {
			return response.InternalError(fmt.Errorf("Storage pool not found: %w", err))
		}

		var profile *api.Profile

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Otherwise try and restore to the project's default profile pool.
			_, profile, err = tx.GetProfile(ctx, bInfo.Project, "default")

			return err
		})
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get default profile: %w", err))
		}

		_, v, err := instancetype.GetRootDiskDevice(profile.Devices)
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

		pool, err := storagePools.LoadByName(s, bInfo.Pool)
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

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", bInfo.Pool, "volumes", string(bInfo.Type), bInfo.Name)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.CustomVolumeBackupRestore, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}

// ctxStorageVolumeDetails is the request.CtxKey corresponding to storageVolumeDetails, which is added to the request
// context in addStoragePoolVolumeDetailsToRequestContext.
const ctxStorageVolumeDetails request.CtxKey = "storage-volume-details"

// storageVolumeDetails contains details common to all storage volume requests. A value of this type is added to the
// request context when addStoragePoolVolumeDetailsToRequestContext is called. We do this to avoid repeated logic when
// parsing the request details and/or making database calls to get the storage pool or effective project. These fields
// are required for the storage volume access check, and are subsequently available in the storage volume handlers.
type storageVolumeDetails struct {
	volumeName         string
	volumeTypeName     string
	volumeType         int
	pool               storagePools.Pool
	forwardingNodeInfo *db.NodeInfo
}

// addStoragePoolVolumeDetailsToRequestContext extracts storageVolumeDetails from the http.Request and adds it to the
// request context with the ctxStorageVolumeDetails request.CtxKey. Additionally, the effective project of the storage
// bucket is added to the request context under request.CtxEffectiveProjectName.
func addStoragePoolVolumeDetailsToRequestContext(s *state.State, r *http.Request) error {
	var details storageVolumeDetails
	defer func() {
		request.SetCtxValue(r, ctxStorageVolumeDetails, details)
	}()

	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return err
	}

	details.volumeName = volumeName

	if shared.IsSnapshot(volumeName) {
		return api.StatusErrorf(http.StatusBadRequest, "Invalid storage volume %q", volumeName)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return err
	}

	details.volumeTypeName = volumeTypeName

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Failed to get storage volume type: %w", err)
	}

	details.volumeType = volumeType

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return err
	}

	// Load the storage pool containing the volume. This is required by the access handler as all remote volumes
	// do not have a location (regardless of whether the caller used a target parameter to send the request to a
	// particular member).
	storagePool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	details.pool = storagePool

	// Get the effective project.
	effectiveProject, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return fmt.Errorf("Failed to get effective project name: %w", err)
	}

	request.SetCtxValue(r, request.CtxEffectiveProjectName, effectiveProject)

	// If the target is set, we have all the information we need to perform the access check.
	if request.QueryParam(r, "target") != "" {
		return nil
	}

	// If the request has already been forwarded, no reason to perform further logic to determine the location of the
	// volume.
	_, err = request.GetCtxValue[string](r.Context(), request.CtxForwardedProtocol)
	if err == nil {
		return nil
	}

	// Get information about the cluster member containing the volume.
	remoteNodeInfo, err := getRemoteVolumeNodeInfo(r.Context(), s, poolName, effectiveProject, volumeName, volumeType)
	if err != nil {
		return err
	}

	details.forwardingNodeInfo = remoteNodeInfo

	return nil
}

// getRemoteVolumeNodeInfo figures out the cluster member on which the volume with the given name is defined. If it is
// the local cluster member it returns nil and no error. If it is another cluster member it returns a db.NodeInfo containing
// the name and address of the remote member. If there is more than one cluster member with a matching volume name, an
// error is returned.
func getRemoteVolumeNodeInfo(ctx context.Context, s *state.State, poolName string, projectName string, volumeName string, volumeType int) (*db.NodeInfo, error) {
	localNodeID := s.DB.Cluster.GetNodeID()
	var err error
	var nodes []db.NodeInfo
	var poolID int64
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		poolID, err = tx.GetStoragePoolID(ctx, poolName)
		if err != nil {
			return err
		}

		nodes, err = tx.GetStorageVolumeNodes(ctx, poolID, projectName, volumeName, volumeType)
		if err != nil && !errors.Is(err, db.ErrNoClusterMember) {
			return err
		} else if err == nil {
			return nil
		}

		// If we couldn't get the nodes directly, get the volume for a subsequent check.
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volumeName, true)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// If volume uses a remote storage driver and so has no explicit cluster member, then we need to check
	// whether it is exclusively attached to remote instance, and if so then we need to forward the request to
	// the node where it is currently used. This avoids conflicting with another member when using it locally.
	if dbVolume != nil {
		remoteInstance, err := storagePools.VolumeUsedByExclusiveRemoteInstancesWithProfiles(s, poolName, projectName, &dbVolume.StorageVolume)
		if err != nil {
			return nil, fmt.Errorf("Failed checking if volume %q is available: %w", volumeName, err)
		}

		if remoteInstance == nil {
			// Volume isn't exclusively attached to an instance. Use local cluster member.
			return nil, nil
		}

		var instNode db.NodeInfo
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			instNode, err = tx.GetNodeByName(ctx, remoteInstance.Node)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("Failed getting cluster member info for %q: %w", remoteInstance.Node, err)
		}

		// Replace node list with instance's cluster member node (which might be local member).
		nodes = []db.NodeInfo{instNode}
	}

	nodeCount := len(nodes)
	if nodeCount > 1 {
		return nil, fmt.Errorf("More than one cluster member has a volume named %q. Please target a specific member", volumeName)
	} else if nodeCount < 1 {
		// Should never get here.
		return nil, fmt.Errorf("Volume %q has empty cluster member list", volumeName)
	}

	node := nodes[0]
	if node.ID == localNodeID {
		// Use local cluster member if volume belongs to this local member.
		return nil, nil
	}

	// Connect to remote cluster member.
	return &node, nil
}
