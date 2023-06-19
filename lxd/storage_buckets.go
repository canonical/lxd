package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolBucketsCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets",

	Get:  APIEndpointAction{Handler: storagePoolBucketsGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolBucketsPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolBucketCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets/{bucketName}",

	Delete: APIEndpointAction{Handler: storagePoolBucketDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolBucketGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolBucketPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolBucketPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolBucketKeysCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets/{bucketName}/keys",

	Get:  APIEndpointAction{Handler: storagePoolBucketKeysGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolBucketKeysPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolBucketKeyCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets/{bucketName}/keys/{keyName}",

	Delete: APIEndpointAction{Handler: storagePoolBucketKeyDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolBucketKeyGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Put:    APIEndpointAction{Handler: storagePoolBucketKeyPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
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
	requestProjectName := projectParam(r)
	bucketProjectName, err := project.StorageBucketProject(r.Context(), d.State().DB.Cluster, requestProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	driverInfo := pool.Driver().Info()
	if !driverInfo.Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool driver %q does not support buckets", driverInfo.Name))
	}

	memberSpecific := false // Get buckets for all cluster members.

	var dbBuckets []*db.StorageBucket

	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	// Sort by bucket name.
	sort.SliceStable(dbBuckets, func(i, j int) bool {
		bucketA := dbBuckets[i]
		bucketB := dbBuckets[j]

		return bucketA.Name < bucketB.Name
	})

	if util.IsRecursionRequest(r) {
		buckets := make([]*api.StorageBucket, 0, len(dbBuckets))
		for _, dbBucket := range dbBuckets {
			u := pool.GetBucketURL(dbBucket.Name)
			if u != nil {
				dbBucket.S3URL = u.String()
			}

			buckets = append(buckets, &dbBucket.StorageBucket)
		}

		return response.SyncResponse(true, buckets)
	}

	urls := make([]string, 0, len(dbBuckets))
	for _, dbBucket := range dbBuckets {
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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
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

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := queryParam(r, "target")
	memberSpecific := targetMember != ""

	var bucket *db.StorageBucket
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), bucketProjectName, memberSpecific, bucketName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	u := pool.GetBucketURL(bucket.Name)
	if u != nil {
		bucket.S3URL = u.String()
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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.StorageBucketPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		targetMember := queryParam(r, "target")
		memberSpecific := targetMember != ""

		var bucket *db.StorageBucket
		err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			bucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), bucketProjectName, memberSpecific, bucketName)
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range bucket.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	err = pool.UpdateBucket(bucketProjectName, bucketName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating storage bucket: %w", err))
	}

	s.Events.SendLifecycle(bucketProjectName, lifecycle.StorageBucketUpdated.Event(pool, bucketProjectName, bucketName, request.CreateRequestor(r), nil))

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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
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

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	err = pool.DeleteBucket(bucketProjectName, bucketName, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting storage bucket: %w", err))
	}

	s.Events.SendLifecycle(bucketProjectName, lifecycle.StorageBucketDeleted.Event(pool, bucketProjectName, bucketName, request.CreateRequestor(r), nil))

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
	bucketProjectName, err := project.StorageBucketProject(r.Context(), d.State().DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	driverInfo := pool.Driver().Info()
	if !driverInfo.Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool driver %q does not support buckets", driverInfo.Name))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	memberSpecific := false // Get buckets for all cluster members.

	var dbBucket *db.StorageBucket
	var dbBucketKeys []*db.StorageBucketKey
	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbBucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), bucketProjectName, memberSpecific, bucketName)
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
		bucketKeyURLs = append(bucketKeyURLs, dbBucketKey.URL(version.APIVersion, poolName, bucketProjectName, bucketName).String())
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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.StorageBucketKeysPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	key, err := pool.CreateBucketKey(bucketProjectName, bucketName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating storage bucket key: %w", err))
	}

	lc := lifecycle.StorageBucketKeyCreated.Event(pool, bucketProjectName, pool.Name(), req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(bucketProjectName, lc)

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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
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

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	keyName, err := url.PathUnescape(mux.Vars(r)["keyName"])
	if err != nil {
		return response.SmartError(err)
	}

	err = pool.DeleteBucketKey(bucketProjectName, bucketName, keyName, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting storage bucket key: %w", err))
	}

	s.Events.SendLifecycle(bucketProjectName, lifecycle.StorageBucketKeyDeleted.Event(pool, bucketProjectName, pool.Name(), bucketName, request.CreateRequestor(r), nil))

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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
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

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	keyName, err := url.PathUnescape(mux.Vars(r)["keyName"])
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := queryParam(r, "target")
	memberSpecific := targetMember != ""

	var bucketKey *db.StorageBucketKey
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err := tx.GetStoragePoolBucket(ctx, pool.ID(), bucketProjectName, memberSpecific, bucketName)
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

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, projectParam(r))
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

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
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

	err = pool.UpdateBucketKey(bucketProjectName, bucketName, keyName, req, nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating storage bucket key: %w", err))
	}

	s.Events.SendLifecycle(bucketProjectName, lifecycle.StorageBucketKeyUpdated.Event(pool, bucketProjectName, pool.Name(), bucketName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
