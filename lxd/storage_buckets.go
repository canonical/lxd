package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

var storagePoolBucketsCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/buckets",
	MetricsType: entity.TypeStoragePool,

	Get:  APIEndpointAction{Handler: storagePoolBucketsGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: storagePoolBucketsPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateStorageBuckets)},
}

var storagePoolBucketCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/buckets/{bucketName}",
	MetricsType: entity.TypeStoragePool,

	Delete: APIEndpointAction{Handler: storagePoolBucketDelete, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: storagePoolBucketGet, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanView)},
	Patch:  APIEndpointAction{Handler: storagePoolBucketPut, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: storagePoolBucketPut, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanEdit)},
}

var storagePoolBucketKeysCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/buckets/{bucketName}/keys",
	MetricsType: entity.TypeStoragePool,

	Get:  APIEndpointAction{Handler: storagePoolBucketKeysGet, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanView)},
	Post: APIEndpointAction{Handler: storagePoolBucketKeysPost, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanEdit)},
}

var storagePoolBucketKeyCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/buckets/{bucketName}/keys/{keyName}",
	MetricsType: entity.TypeStoragePool,

	Delete: APIEndpointAction{Handler: storagePoolBucketKeyDelete, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanEdit)},
	Get:    APIEndpointAction{Handler: storagePoolBucketKeyGet, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanView)},
	Put:    APIEndpointAction{Handler: storagePoolBucketKeyPut, AccessHandler: storageBucketAccessHandler(auth.EntitlementCanEdit)},
}

// storageBucketAccessHandler returns an access handler that checks for the given entitlement against a storage bucket.
// The storage pool containing the bucket and the effective project of the bucket are added to the request context for
// later use.
func storageBucketAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()

		err := addStorageBucketDetailsToContext(d, r)
		if err != nil {
			return response.SmartError(err)
		}

		details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
		if err != nil {
			return nil
		}

		// If the storage pool is a remote driver, the auth subsystem does not require a target parameter to create a
		// unique URL for the storage bucket. So even if the caller supplied a target parameter, we don't use it in the
		// access check if the pool is remote.
		target := ""
		if !details.pool.Driver().Info().Remote {
			target = request.QueryParam(r, "target")
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.StorageBucketURL(request.ProjectParam(r), target, details.pool.Name(), details.bucketName), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

// API endpoints

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets storage storage_pool_buckets_get
//
//  Get the storage pool buckets
//
//  Returns a list of storage pool buckets (URLs).
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
//                "/1.0/storage-pools/default/buckets/foo",
//                "/1.0/storage-pools/default/buckets/bar",
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets?recursion=1 storage storage_pool_buckets_get_recursion1
//
//	Get the storage pool buckets
//
//	Returns a list of storage pool buckets (structs).
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
//	          description: List of storage pool buckets
//	          items:
//	            $ref: "#/definitions/StorageBucket"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	requestProjectName := request.ProjectParam(r)
	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, requestProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeStorageBucket, true)
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	driverInfo := pool.Driver().Info()
	if !driverInfo.Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool driver %q does not support buckets", driverInfo.Name))
	}

	memberSpecific := false // Get buckets for all cluster members.

	var dbBuckets []*db.StorageBucket

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		poolID := pool.ID()
		filters := []db.StorageBucketFilter{{
			PoolID:  &poolID,
			Project: &bucketProjectName,
		}}

		dbBuckets, err = tx.GetStoragePoolBuckets(ctx, memberSpecific, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage buckets: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	request.SetCtxValue(r, request.CtxEffectiveProjectName, bucketProjectName)
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeStorageBucket)
	if err != nil {
		return response.SmartError(err)
	}

	filteredDBBuckets := make([]*db.StorageBucket, 0)

	for _, bucket := range dbBuckets {
		if !userHasPermission(entity.StorageBucketURL(requestProjectName, bucket.Location, poolName, bucket.Name)) {
			continue
		}

		filteredDBBuckets = append(filteredDBBuckets, bucket)
	}

	// Sort by bucket name.
	sort.SliceStable(filteredDBBuckets, func(i, j int) bool {
		bucketA := filteredDBBuckets[i]
		bucketB := filteredDBBuckets[j]

		return bucketA.Name < bucketB.Name
	})

	if util.IsRecursionRequest(r) {
		buckets := make([]*api.StorageBucket, 0, len(filteredDBBuckets))
		urlToStorageBucket := make(map[*api.URL]auth.EntitlementReporter, len(filteredDBBuckets))
		for _, dbBucket := range filteredDBBuckets {
			u := pool.GetBucketURL(dbBucket.Name)
			if u != nil {
				dbBucket.S3URL = u.String()
			}

			buckets = append(buckets, &dbBucket.StorageBucket)
			urlToStorageBucket[entity.StorageBucketURL(dbBucket.Project, dbBucket.Location, dbBucket.PoolName, dbBucket.Name)] = &dbBucket.StorageBucket
		}

		if len(withEntitlements) > 0 {
			err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypeStorageBucket, withEntitlements, urlToStorageBucket)
			if err != nil {
				return response.SmartError(err)
			}
		}

		return response.SyncResponse(true, buckets)
	}

	urls := make([]string, 0, len(filteredDBBuckets))
	for _, dbBucket := range filteredDBBuckets {
		urls = append(urls, dbBucket.StorageBucket.URL(version.APIVersion, poolName, requestProjectName).String())
	}

	return response.SyncResponse(true, urls)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName} storage storage_pool_bucket_get
//
//	Get the storage pool bucket
//
//	Gets a specific storage pool bucket.
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
//	    description: Storage pool bucket
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
//	          $ref: "#/definitions/StorageBucket"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeStorageBucket, false)
	if err != nil {
		return response.SmartError(err)
	}

	if !details.pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	var bucket *db.StorageBucket
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, details.pool.ID(), effectiveProjectName, memberSpecific, details.bucketName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	u := details.pool.GetBucketURL(bucket.Name)
	if u != nil {
		bucket.S3URL = u.String()
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypeStorageBucket, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.StorageBucketURL(effectiveProjectName, bucket.Location, details.pool.Name(), bucket.Name): &bucket.StorageBucket})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, bucket, bucket.Etag())
}

// swagger:operation POST /1.0/storage-pools/{poolName}/buckets storage storage_pool_bucket_post
//
//	Add a storage pool bucket.
//
//	Creates a new storage pool bucket.
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
//	    name: bucket
//	    description: Bucket
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageBucketsPost"
//	responses:
//	  "200":
//	    $ref: '#/definitions/StorageBucketKey'
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	if r.Header.Get("Content-Type") == "application/octet-stream" {
		return createStoragePoolBucketFromBackup(s, r, request.ProjectParam(r), bucketProjectName, r.Body, poolName, r.Header.Get("X-LXD-name"))
	}

	// Parse the request into a record.
	req := api.StorageBucketsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	revert := revert.New()
	defer revert.Fail()

	err = pool.CreateBucket(bucketProjectName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating storage bucket: %w", err))
	}

	revert.Add(func() { _ = pool.DeleteBucket(bucketProjectName, req.Name, nil) })

	// Create admin key for new bucket.
	adminKeyReq := api.StorageBucketKeysPost{
		StorageBucketKeyPut: api.StorageBucketKeyPut{
			Role:        "admin",
			Description: "Admin user",
		},
		Name: "admin",
	}

	adminKey, err := pool.CreateBucketKey(bucketProjectName, req.Name, adminKeyReq, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating storage bucket admin key: %w", err))
	}

	s.Events.SendLifecycle(bucketProjectName, lifecycle.StorageBucketCreated.Event(pool, bucketProjectName, req.Name, request.CreateRequestor(r), nil))

	u := api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", req.Name)

	revert.Success()
	return response.SyncResponseLocation(true, adminKey, u.String())
}

// swagger:operation PATCH /1.0/storage-pools/{name}/buckets/{bucketName} storage storage_pool_bucket_patch
//
//  Partially update the storage bucket.
//
//  Updates a subset of the storage bucket configuration.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//    - in: body
//      name: storage bucket
//      description: Storage bucket configuration
//      required: true
//      schema:
//        $ref: "#/definitions/StorageBucketPut"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "412":
//      $ref: "#/responses/PreconditionFailed"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation PUT /1.0/storage-pools/{name}/buckets/{bucketName} storage storage_pool_bucket_put
//
//	Update the storage bucket
//
//	Updates the entire storage bucket configuration.
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
//	    name: storage bucket
//	    description: Storage bucket configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageBucketPut"
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
func storagePoolBucketPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	// Get existing storage bucket.
	var bucket *db.StorageBucket
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, details.pool.ID(), effectiveProjectName, memberSpecific, details.bucketName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = util.EtagCheck(r, bucket.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.StorageBucketPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range bucket.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	err = details.pool.UpdateBucket(effectiveProjectName, details.bucketName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating storage bucket: %w", err))
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageBucketUpdated.Event(details.pool, effectiveProjectName, details.bucketName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{name}/buckets/{bucketName} storage storage_pool_bucket_delete
//
//	Delete the storage bucket
//
//	Removes the storage bucket.
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
func storagePoolBucketDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	err = details.pool.DeleteBucket(effectiveProjectName, details.bucketName, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting storage bucket: %w", err))
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageBucketDeleted.Event(details.pool, effectiveProjectName, details.bucketName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// API endpoints

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/keys storage storage_pool_bucket_keys_get
//
//  Get the storage pool bucket keys
//
//  Returns a list of storage pool bucket keys (URLs).
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
//                "/1.0/storage-pools/default/buckets/foo/keys/my-read-only-key",
//                "/1.0/storage-pools/default/buckets/bar/keys/admin",
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/keys?recursion=1 storage storage_pool_bucket_keys_get_recursion1
//
//	Get the storage pool bucket keys
//
//	Returns a list of storage pool bucket keys (structs).
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
//	          description: List of storage pool bucket keys
//	          items:
//	            $ref: "#/definitions/StorageBucketKey"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketKeysGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	driverInfo := details.pool.Driver().Info()
	if !driverInfo.Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool driver %q does not support buckets", driverInfo.Name))
	}

	// If target is set, get buckets only for this cluster members.
	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	var dbBucket *db.StorageBucket
	var dbBucketKeys []*db.StorageBucketKey
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbBucket, err = tx.GetStoragePoolBucket(ctx, details.pool.ID(), effectiveProjectName, memberSpecific, details.bucketName)
		if err != nil {
			return fmt.Errorf("Failed loading storage bucket: %w", err)
		}

		dbBucketKeys, err = tx.GetStoragePoolBucketKeys(ctx, dbBucket.ID)
		if err != nil {
			return fmt.Errorf("Failed loading storage bucket keys: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if util.IsRecursionRequest(r) {
		bucketKeys := make([]*api.StorageBucketKey, 0, len(dbBucketKeys))
		for _, dbBucketKey := range dbBucketKeys {
			bucketKeys = append(bucketKeys, &dbBucketKey.StorageBucketKey)
		}

		return response.SyncResponse(true, bucketKeys)
	}

	bucketKeyURLs := make([]string, 0, len(dbBucketKeys))
	for _, dbBucketKey := range dbBucketKeys {
		bucketKeyURLs = append(bucketKeyURLs, dbBucketKey.URL(version.APIVersion, details.pool.Name(), effectiveProjectName, details.bucketName).String())
	}

	return response.SyncResponse(true, bucketKeyURLs)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/buckets/{bucketName}/keys storage storage_pool_bucket_key_post
//
//	Add a storage pool bucket key.
//
//	Creates a new storage pool bucket key.
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
//	    name: bucket
//	    description: Bucket
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageBucketKeysPost"
//	responses:
//	  "200":
//	    $ref: '#/definitions/StorageBucketKey'
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketKeysPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.StorageBucketKeysPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	key, err := details.pool.CreateBucketKey(effectiveProjectName, details.bucketName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating storage bucket key: %w", err))
	}

	lc := lifecycle.StorageBucketKeyCreated.Event(details.pool, effectiveProjectName, details.pool.Name(), req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(effectiveProjectName, lc)

	return response.SyncResponseLocation(true, key, lc.Source)
}

// swagger:operation DELETE /1.0/storage-pools/{name}/buckets/{bucketName}/keys/{keyName} storage storage_pool_bucket_key_delete
//
//	Delete the storage bucket key
//
//	Removes the storage bucket key.
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
func storagePoolBucketKeyDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	keyName, err := url.PathUnescape(mux.Vars(r)["keyName"])
	if err != nil {
		return response.SmartError(err)
	}

	err = details.pool.DeleteBucketKey(effectiveProjectName, details.bucketName, keyName, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting storage bucket key: %w", err))
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageBucketKeyDeleted.Event(details.pool, effectiveProjectName, details.pool.Name(), details.bucketName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/keys/{keyName} storage storage_pool_bucket_key_get
//
//	Get the storage pool bucket key
//
//	Gets a specific storage pool bucket key.
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
//	    description: Storage pool bucket key
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
//	          $ref: "#/definitions/StorageBucketKey"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketKeyGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	if !details.pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	keyName, err := url.PathUnescape(mux.Vars(r)["keyName"])
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	var bucketKey *db.StorageBucketKey
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err := tx.GetStoragePoolBucket(ctx, details.pool.ID(), effectiveProjectName, memberSpecific, details.bucketName)
		if err != nil {
			return err
		}

		bucketKey, err = tx.GetStoragePoolBucketKey(ctx, bucket.ID, keyName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, bucketKey.StorageBucketKey, bucketKey.Etag())
}

// swagger:operation PUT /1.0/storage-pools/{name}/buckets/{bucketName}/keys/{keyName} storage storage_pool_bucket_key_put
//
//	Update the storage bucket key
//
//	Updates the entire storage bucket key configuration.
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
//	    name: storage bucket
//	    description: Storage bucket key configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageBucketKeyPut"
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
func storagePoolBucketKeyPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageBucketDetails](r.Context(), ctxStorageBucketDetails)
	if err != nil {
		return response.SmartError(err)
	}

	keyName, err := url.PathUnescape(mux.Vars(r)["keyName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.StorageBucketKeyPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = details.pool.UpdateBucketKey(effectiveProjectName, details.bucketName, keyName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating storage bucket key: %w", err))
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageBucketKeyUpdated.Event(details.pool, effectiveProjectName, details.pool.Name(), details.bucketName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// ctxStorageBucketDetails is the request.CtxKey corresponding to storageBucketDetails, which is added to the request
// context in addStorageBucketDetailsToContext.
const ctxStorageBucketDetails request.CtxKey = "storage-bucket-details"

// storageBucketDetails contains details common to all storage volume requests. A value of this type is added to the
// request context when addStorageBucketDetailsToContext is called. We do this to avoid repeated logic when
// parsing the request details and/or making database calls to get the storage pool or effective project. These fields
// are required for the storage bucket access check, and are subsequently available in the storage bucket handlers.
type storageBucketDetails struct {
	bucketName string
	pool       storagePools.Pool
}

// addStorageBucketDetailsToContext extracts storageBucketDetails from the http.Request and adds it to the
// request context with the ctxStorageBucketDetails request.CtxKey. Additionally, the effective project of the storage
// bucket is added to the request context under request.CtxEffectiveProjectName.
func addStorageBucketDetailsToContext(d *Daemon, r *http.Request) error {
	var details storageBucketDetails
	defer func() {
		request.SetCtxValue(r, ctxStorageBucketDetails, details)
	}()

	s := d.State()

	projectName := request.ProjectParam(r)

	effectiveProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectName)
	if err != nil {
		return err
	}

	request.SetCtxValue(r, request.CtxEffectiveProjectName, effectiveProjectName)

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return fmt.Errorf("Failed loading storage pool: %w", err)
	}

	details.pool = pool

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return err
	}

	details.bucketName = bucketName
	return nil
}

func createStoragePoolBucketFromBackup(s *state.State, r *http.Request, requestProjectName string, projectName string, data io.Reader, pool string, bucketName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Create temporary file to store uploaded backup data.
	backupFile, err := os.CreateTemp(shared.VarPath("backups"), backup.WorkingDirPrefix+"_")
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

	// Override bucket name.
	if bucketName != "" {
		bInfo.Name = bucketName
	}

	logger.Debug("Backup file info loaded", logger.Ctx{
		"type":    bInfo.Type,
		"name":    bInfo.Name,
		"project": bInfo.Project,
		"backend": bInfo.Backend,
		"pool":    bInfo.Pool,
	})

	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer func() { _ = backupFile.Close() }()
		defer runRevert.Fail()

		pool, err := storagePools.LoadByName(s, bInfo.Pool)
		if err != nil {
			return err
		}

		err = pool.CreateBucketFromBackup(*bInfo, backupFile, nil)
		if err != nil {
			return fmt.Errorf("Create storage bucket from backup: %w", err)
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_buckets"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", bInfo.Pool, "buckets", string(bInfo.Type), bInfo.Name)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.BucketBackupRestore, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}
