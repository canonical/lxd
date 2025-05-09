package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
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
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// Lock to prevent concurent storage pools creation.
var storagePoolCreateLock sync.Mutex

var storagePoolsCmd = APIEndpoint{
	Path:        "storage-pools",
	MetricsType: entity.TypeStoragePool,

	Get:  APIEndpointAction{Handler: storagePoolsGetHandler, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolsPostHandler, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanCreateStoragePools)},
}

var storagePoolCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}",
	MetricsType: entity.TypeStoragePool,

	Delete: APIEndpointAction{Handler: storagePoolDeleteHandler, AccessHandler: allowPermission(entity.TypeStoragePool, auth.EntitlementCanDelete, "poolName")},
	Get:    APIEndpointAction{Handler: storagePoolGetHandler, AccessHandler: allowAuthenticated},
	Patch:  APIEndpointAction{Handler: storagePoolPatchHandler, AccessHandler: allowPermission(entity.TypeStoragePool, auth.EntitlementCanEdit, "poolName")},
	Put:    APIEndpointAction{Handler: storagePoolPutHandler, AccessHandler: allowPermission(entity.TypeStoragePool, auth.EntitlementCanEdit, "poolName")},
}

// swagger:operation GET /1.0/storage-pools storage storage_pools_get
//
//  Get the storage pools
//
//  Returns a list of storage pools (URLs).
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
//                "/1.0/storage-pools/local",
//                "/1.0/storage-pools/remote"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools?recursion=1 storage storage_pools_get_recursion1
//
//	Get the storage pools
//
//	Returns a list of storage pools (structs).
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
//	          description: List of storage pools
//	          items:
//	            $ref: "#/definitions/StoragePool"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolsGetHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if util.IsRecursionRequest(r) {
		withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeStoragePool, true)
		if err != nil {
			return response.SmartError(err)
		}

		pools, err := storagePoolsGet(r.Context(), s, request.ProjectParam(r), withEntitlements)
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, pools)
	}

	urls, err := storagePoolsURLGet(r.Context(), s, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, urls)
}

func storagePoolsGet(reqContext context.Context, s *state.State, requestProjectName string, withEntitlements []auth.Entitlement) ([]*api.StoragePool, error) {
	poolNames, hiddenPoolNames, err := getStoragePoolsDBNames(reqContext, s, requestProjectName)
	if err != nil {
		return nil, err
	}

	hasEditPermission, err := s.Authorizer.GetPermissionChecker(reqContext, auth.EntitlementCanEdit, entity.TypeStoragePool)
	if err != nil {
		return nil, err
	}

	result := []*api.StoragePool{}
	urlToPool := make(map[*api.URL]auth.EntitlementReporter)
	for _, poolName := range poolNames {
		// Hide storage pools with a 0 project limit.
		if slices.Contains(hiddenPoolNames, poolName) {
			continue
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return nil, err
		}

		// Get all users of the storage pool.
		poolUsedBy, err := storagePools.UsedBy(reqContext, s, pool, false, false)
		if err != nil {
			return nil, err
		}

		poolAPI := pool.ToAPI()
		poolAPI.UsedBy = project.FilterUsedBy(reqContext, s.Authorizer, poolUsedBy)

		if !hasEditPermission(entity.StoragePoolURL(poolName)) {
			// Don't allow non-admins to see pool config as sensitive info can be stored there.
			poolAPI.Config = nil
		}

		// If no member is specified and the daemon is clustered, we omit the node-specific fields.
		if s.ServerClustered {
			for _, key := range db.NodeSpecificStorageConfig {
				delete(poolAPI.Config, key)
			}
		} else {
			// Use local status if not clustered. To allow seeing unavailable pools.
			poolAPI.Status = pool.LocalStatus()
		}

		result = append(result, &poolAPI)
		urlToPool[entity.StoragePoolURL(poolName)] = &poolAPI
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(reqContext, s.Authorizer, s.IdentityCache, entity.TypeStorageVolume, withEntitlements, urlToPool)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func storagePoolsURLGet(reqContext context.Context, s *state.State, requestProjectName string) ([]string, error) {
	poolNames, hiddenPoolNames, err := getStoragePoolsDBNames(reqContext, s, requestProjectName)
	if err != nil {
		return nil, err
	}

	result := []string{}
	for _, poolName := range poolNames {
		// Hide storage pools with a 0 project limit.
		if slices.Contains(hiddenPoolNames, poolName) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, poolName))
	}

	return result, nil
}

// getStoragePoolsDBNames returns the storage pool names and hidden storage pool names for the given project.
func getStoragePoolsDBNames(ctx context.Context, s *state.State, reqProjectName string) (poolNames []string, hiddenPoolNames []string, err error) {
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load the pool names.
		poolNames, err = tx.GetStoragePoolNames(ctx)
		if err != nil {
			return err
		}

		// Load the project limits.
		hiddenPoolNames, err = limits.HiddenStoragePools(ctx, tx, reqProjectName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil && !response.IsNotFoundError(err) {
		return nil, nil, err
	}

	return poolNames, hiddenPoolNames, nil
}

// swagger:operation POST /1.0/storage-pools storage storage_pools_post
//
//	Add a storage pool
//
//	Creates a new storage pool.
//	When clustered, storage pools require individual POST for each cluster member prior to a global POST.
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
//	    name: storage
//	    description: Storage pool
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolsPostHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	req := api.StoragePoolsPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	targetNode := request.QueryParam(r, "target")
	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	lc, err := storagePoolsPost(r.Context(), s, req, clientType, isClusterNotification(r), targetNode)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, lc.Source)
}

func storagePoolsPost(reqContext context.Context, s *state.State, req api.StoragePoolsPost, clientType clusterRequest.ClientType, isClusterNotification bool, target string) (*api.EventLifecycle, error) {
	// Quick checks.
	if req.Name == "" {
		return nil, api.NewStatusError(http.StatusBadRequest, "No name provided")
	}

	if strings.Contains(req.Name, "/") {
		return nil, api.NewStatusError(http.StatusBadRequest, "Storage pool names may not contain slashes")
	}

	if req.Driver == "" {
		return nil, api.NewStatusError(http.StatusBadRequest, "No driver provided")
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	ctx := logger.Ctx{}
	if target != "" {
		ctx["target"] = target
	}

	storagePoolCreateLock.Lock()
	defer storagePoolCreateLock.Unlock()

	lc := lifecycle.StoragePoolCreated.Event(req.Name, request.CreateRequestor(reqContext), ctx)

	if isClusterNotification {
		// This is an internal request which triggers the actual
		// creation of the pool across all nodes, after they have been
		// previously defined.
		err := storagePoolValidate(s, req.Name, req.Driver, req.Config)
		if err != nil {
			return nil, api.NewStatusError(http.StatusBadRequest, err.Error())
		}

		var poolID int64

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			poolID, err = tx.GetStoragePoolID(ctx, req.Name)

			return err
		})
		if err != nil {
			return nil, err
		}

		_, err = storagePoolCreateLocal(s, poolID, req, clientType)
		if err != nil {
			return nil, err
		}

		return &lc, nil
	}

	if target != "" {
		// A targetNode was specified, let's just define the node's storage without actually creating it.
		// The only legal key values for the storage config are the ones in NodeSpecificStorageConfig.
		for key := range req.Config {
			if !shared.ValueInSlice(key, db.NodeSpecificStorageConfig) {
				return nil, fmt.Errorf("Config key %q may not be used as member-specific key", key)
			}
		}

		err := storagePoolValidate(s, req.Name, req.Driver, req.Config)
		if err != nil {
			return nil, api.NewStatusError(http.StatusBadRequest, err.Error())
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.CreatePendingStoragePool(ctx, target, req.Name, req.Driver, req.Config)
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return nil, api.StatusErrorf(http.StatusBadRequest, "The storage pool already defined on member %q", target)
			}

			return nil, err
		}

		return &lc, nil
	}

	var pool *api.StoragePool

	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load existing pool if exists, if not don't fail.
		_, pool, _, err = tx.GetStoragePoolInAnyState(ctx, req.Name)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		return nil, err
	}

	// Check if we're clustered.
	count, err := cluster.Count(s)
	if err != nil {
		return nil, err
	}

	// No targetNode was specified and we're clustered or there is an existing partially created single node
	// pool, either way finalize the config in the db and actually create the pool on all nodes in the cluster.
	if count > 1 || (pool != nil && pool.Status != api.StoragePoolStatusCreated) {
		err = storagePoolsPostCluster(s, pool, req, clientType)
		if err != nil {
			return nil, err
		}
	} else {
		// Create new single node storage pool.
		err = storagePoolCreateGlobal(s, req, clientType)
		if err != nil {
			return nil, err
		}
	}

	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	return &lc, nil
}

// storagePoolPartiallyCreated returns true of supplied storage pool has properties that indicate it has had
// previous create attempts run on it but failed on one or more nodes.
func storagePoolPartiallyCreated(pool *api.StoragePool) bool {
	// If the pool status is StoragePoolStatusErrored, this means create has been run in the past and has
	// failed on one or more nodes. Hence it is partially created.
	if pool.Status == api.StoragePoolStatusErrored {
		return true
	}

	// If the pool has global config keys, then it has previously been created by having its global config
	// inserted, and this means it is partialled created.
	for key := range pool.Config {
		if !shared.ValueInSlice(key, db.NodeSpecificStorageConfig) {
			return true
		}
	}

	return false
}

// storagePoolsPostCluster handles creating storage pools after the per-node config records have been created.
// Accepts an optional existing pool record, which will exist when performing subsequent re-create attempts.
func storagePoolsPostCluster(s *state.State, pool *api.StoragePool, req api.StoragePoolsPost, clientType clusterRequest.ClientType) error {
	// Check that no node-specific config key has been defined.
	for key := range req.Config {
		if shared.ValueInSlice(key, db.NodeSpecificStorageConfig) {
			return fmt.Errorf("Config key %q is cluster member specific", key)
		}
	}

	// If pool already exists, perform quick checks.
	if pool != nil {
		// Check pool isn't already created.
		if pool.Status == api.StoragePoolStatusCreated {
			return errors.New("The storage pool is already created")
		}

		// Check the requested pool type matches the type created when adding the local member config.
		if req.Driver != pool.Driver {
			return fmt.Errorf("Requested storage pool driver %q doesn't match driver in existing database record %q", req.Driver, pool.Driver)
		}
	}

	// Check that the pool is properly defined, fetch the node-specific configs and insert the global config.
	var configs map[string]map[string]string
	var poolID int64
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Check that the pool was defined at all. Must come before partially created checks.
		poolID, err = tx.GetStoragePoolID(ctx, req.Name)
		if err != nil {
			return err
		}

		// Check if any global config exists already, if so we should not create global config again.
		if pool != nil && storagePoolPartiallyCreated(pool) {
			if len(req.Config) > 0 {
				return errors.New("Storage pool already partially created. Please do not specify any global config when re-running create")
			}

			logger.Debug("Skipping global storage pool create as global config already partially created", logger.Ctx{"pool": req.Name})
			return nil
		}

		// Fetch the node-specific configs and check the pool is defined for all nodes.
		configs, err = tx.GetStoragePoolNodeConfigs(ctx, poolID)
		if err != nil {
			return err
		}

		// Insert the global config keys.
		err = tx.CreateStoragePoolConfig(poolID, 0, req.Config)
		if err != nil {
			return err
		}

		// Assume failure unless we succeed later on.
		return tx.StoragePoolErrored(req.Name)
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			return errors.New("Pool not pending on any node (use --target <node> first)")
		}

		return err
	}

	// Create notifier for other nodes to create the storage pool.
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
	if err != nil {
		return err
	}

	// Create the pool on this node.
	nodeReq := req

	// Merge node specific config items into global config.
	for key, value := range configs[s.ServerName] {
		nodeReq.Config[key] = value
	}

	updatedConfig, err := storagePoolCreateLocal(s, poolID, req, clientType)
	if err != nil {
		return err
	}

	req.Config = updatedConfig
	logger.Debug("Created storage pool on local cluster member", logger.Ctx{"pool": req.Name})

	// Strip node specific config keys from config. Very important so we don't forward node-specific config.
	for _, k := range db.NodeSpecificStorageConfig {
		delete(req.Config, k)
	}

	// Notify all other nodes to create the pool.
	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		nodeReq := req

		// Clone fresh node config so we don't modify req.Config with this node's specific config which
		// could result in it being sent to other nodes later.
		nodeReq.Config = make(map[string]string, len(req.Config))
		for k, v := range req.Config {
			nodeReq.Config[k] = v
		}

		// Merge node specific config items into global config.
		for key, value := range configs[member.Name] {
			nodeReq.Config[key] = value
		}

		err = client.CreateStoragePool(nodeReq)
		if err != nil {
			return err
		}

		logger.Debug("Created storage pool on cluster member", logger.Ctx{"pool": req.Name, "member": member.Name})

		return nil
	})
	if err != nil {
		return err
	}

	// Finally update the storage pool state.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.StoragePoolCreated(req.Name)
	})
	if err != nil {
		return err
	}

	logger.Debug("Marked storage pool global status as created", logger.Ctx{"pool": req.Name})

	return nil
}

// swagger:operation GET /1.0/storage-pools/{poolName} storage storage_pool_get
//
//	Get the storage pool
//
//	Gets a specific storage pool.
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
//	          $ref: "#/definitions/StoragePool"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolGetHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeStoragePool, false)
	if err != nil {
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")

	pool, etag, err := storagePoolGet(r.Context(), s, poolName, request.ProjectParam(r), withEntitlements, target)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, pool, etag)
}

func storagePoolGet(reqContext context.Context, s *state.State, poolName string, requestProjectName string, withEntitlements []auth.Entitlement, target string) (storagePool *api.StoragePool, etag any, err error) {
	// If a target was specified, forward the request to the relevant node.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return nil, nil, err
	}

	memberSpecific := target != ""

	var hiddenPoolNames []string
	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load the project limits.
		hiddenPoolNames, err = limits.HiddenStoragePools(ctx, tx, requestProjectName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Hide storage pools with a 0 project limit.
	if slices.Contains(hiddenPoolNames, poolName) {
		return nil, nil, api.NewGenericStatusError(http.StatusNotFound)
	}

	// Get the existing storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return nil, nil, err
	}

	// Get all users of the storage pool.
	poolUsedBy, err := storagePools.UsedBy(reqContext, s, pool, false, memberSpecific)
	if err != nil {
		return nil, nil, err
	}

	poolAPI := pool.ToAPI()
	poolAPI.UsedBy = project.FilterUsedBy(reqContext, s.Authorizer, poolUsedBy)

	err = s.Authorizer.CheckPermission(reqContext, entity.StoragePoolURL(poolName), auth.EntitlementCanEdit)
	if err != nil && !auth.IsDeniedError(err) {
		return nil, nil, err
	} else if err != nil {
		// Only allow users that can edit storage pool config to view it as sensitive info can be stored there.
		poolAPI.Config = nil
	}

	// If no member is specified and the daemon is clustered, we omit the node-specific fields.
	if s.ServerClustered && !memberSpecific {
		for _, key := range db.NodeSpecificStorageConfig {
			delete(poolAPI.Config, key)
		}
	} else {
		// Use local status if not clustered or memberSpecific. To allow seeing unavailable pools.
		poolAPI.Status = pool.LocalStatus()
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(reqContext, s.Authorizer, s.IdentityCache, entity.TypeStoragePool, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.StoragePoolURL(poolName): &poolAPI})
		if err != nil {
			return nil, nil, err
		}
	}

	etag = []any{pool.Name(), pool.Driver().Info().Name, pool.Description(), poolAPI.Config}

	return &poolAPI, etag, nil
}

// swagger:operation PUT /1.0/storage-pools/{poolName} storage storage_pool_put
//
//	Update the storage pool
//
//	Updates the entire storage pool configuration.
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
//	    name: storage pool
//	    description: Storage pool configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolPut"
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
func storagePoolPutHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.StoragePoolPut{}
	err = request.DecodeAndRestoreJSONBody(r, &req)
	if err != nil {
		return response.SmartError(err)
	}

	etag := r.Header.Get("If-Match")
	target := request.QueryParam(r, "target")
	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = storagePoolPut(r.Context(), s, poolName, req, etag, clientType, target, false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func storagePoolPut(reqContext context.Context, s *state.State, poolName string, req api.StoragePoolPut, reqETag string, clientType clusterRequest.ClientType, target string, patchConfig bool) error {
	// If a target was specified, forward the request to the relevant node.
	err := forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return err
	}

	// Get the existing storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	if target == "" && pool.Status() != api.StoragePoolStatusCreated {
		return api.NewStatusError(http.StatusBadRequest, "Cannot update storage pool global config when not in created state")
	}

	// Duplicate config for etag modification and generation.
	etagConfig := util.CopyConfig(pool.Driver().Config())

	// If no target node is specified and the daemon is clustered, we omit the node-specific fields so that
	// the e-tag can be generated correctly. This is because the GET request used to populate the request
	// will also remove node-specific keys when no target is specified.
	if target == "" && s.ServerClustered {
		for _, key := range db.NodeSpecificStorageConfig {
			delete(etagConfig, key)
		}
	}

	// Validate the ETag.
	etag := []any{pool.Name(), pool.Driver().Info().Name, pool.Description(), etagConfig}

	err = util.EtagCheckString(reqETag, etag)
	if err != nil {
		return api.NewStatusError(http.StatusPreconditionFailed, err.Error())
	}

	// In clustered mode, we differentiate between node specific and non-node specific config keys based on
	// whether the user has specified a target to apply the config to.
	if s.ServerClustered {
		if target == "" {
			// If no target is specified, then ensure only non-node-specific config keys are changed.
			for k := range req.Config {
				if shared.ValueInSlice(k, db.NodeSpecificStorageConfig) {
					return api.StatusErrorf(http.StatusBadRequest, "Config key %q is cluster member specific", k)
				}
			}
		} else {
			curConfig := pool.Driver().Config()

			// If a target is specified, then ensure only node-specific config keys are changed.
			for k, v := range req.Config {
				if !shared.ValueInSlice(k, db.NodeSpecificStorageConfig) && curConfig[k] != v {
					return api.StatusErrorf(http.StatusBadRequest, "Config key %q may not be used as cluster member specific key", k)
				}
			}
		}
	}

	err = doStoragePoolUpdate(s, pool, req, target, clientType, patchConfig)

	requestor := request.CreateRequestor(reqContext)

	ctx := logger.Ctx{}
	if target != "" {
		ctx["target"] = target
	}

	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.StoragePoolUpdated.Event(pool.Name(), requestor, ctx))

	return err
}

// swagger:operation PATCH /1.0/storage-pools/{poolName} storage storage_pool_patch
//
//	Partially update the storage pool
//
//	Updates a subset of the storage pool configuration.
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
//	    name: storage pool
//	    description: Storage pool configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolPut"
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
func storagePoolPatchHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.StoragePoolPut{}
	err = request.DecodeAndRestoreJSONBody(r, &req)
	if err != nil {
		return response.SmartError(err)
	}

	etag := r.Header.Get("If-Match")
	target := request.QueryParam(r, "target")
	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	patchConfig := true

	err = storagePoolPut(r.Context(), s, poolName, req, etag, clientType, target, patchConfig)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// doStoragePoolUpdate takes the current local storage pool config, merges with the requested storage pool config,
// validates and applies the changes. Will also notify other cluster nodes of non-node specific config if needed.
func doStoragePoolUpdate(s *state.State, pool storagePools.Pool, req api.StoragePoolPut, targetNode string, clientType clusterRequest.ClientType, patchConfig bool) error {
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Normally a "put" request will replace all existing config, however when clustered, we need to account
	// for the node specific config keys and not replace them when the request doesn't specify a specific node.
	if patchConfig {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range pool.Driver().Config() {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	} else if s.ServerClustered && targetNode == "" {
		// If non-node specific config being updated via "put" method in cluster, then merge the current
		// node-specific network config with the submitted config to allow validation.
		// This allows removal of non-node specific keys when they are absent from request config.
		for k, v := range pool.Driver().Config() {
			if shared.ValueInSlice(k, db.NodeSpecificStorageConfig) {
				req.Config[k] = v
			}
		}
	}

	// Validate the configuration.
	err := pool.Validate(req.Config)
	if err != nil {
		return api.NewStatusError(http.StatusBadRequest, err.Error())
	}

	// Notify the other nodes, unless this is itself a notification.
	if s.ServerClustered && clientType != clusterRequest.ClientTypeNotifier && targetNode == "" {
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		sendPool := req
		sendPool.Config = make(map[string]string)
		for k, v := range req.Config {
			// Don't forward node specific keys (these will be merged in on recipient node).
			if shared.ValueInSlice(k, db.NodeSpecificStorageConfig) {
				continue
			}

			sendPool.Config[k] = v
		}

		err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
			return client.UpdateStoragePool(pool.Name(), sendPool, "")
		})
		if err != nil {
			return err
		}
	}

	err = pool.Update(clientType, req.Description, req.Config, nil)
	if err != nil {
		return err
	}

	return nil
}

// swagger:operation DELETE /1.0/storage-pools/{poolName} storage storage_pools_delete
//
//	Delete the storage pool
//
//	Removes the storage pool.
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
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolDeleteHandler(d *Daemon, r *http.Request) response.Response {
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	clusterNotification := isClusterNotification(r)

	err = storagePoolDelete(r.Context(), d.State(), poolName, clientType, clusterNotification)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func storagePoolDelete(reqContext context.Context, s *state.State, poolName string, clientType clusterRequest.ClientType, isClusterNotification bool) error {
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	var notifier cluster.Notifier
	if !isClusterNotification {
		// Quick checks.
		inUse, err := pool.IsUsed()
		if err != nil {
			return err
		}

		if inUse {
			return api.NewStatusError(http.StatusBadRequest, "The storage pool is currently in use")
		}

		// Get the cluster notifier
		notifier, err = cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}
	}

	// Only perform the deletion of remote image volumes on the server handling the request.
	// Otherwise delete local image volumes on each server.
	if !isClusterNotification || !pool.Driver().Info().Remote {
		var removeImgFingerprints []string

		err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
			// Get all the volumes using the storage pool on this server.
			// Only image volumes should remain now.
			poolID := pool.ID() // Create local variable to get the pointer.
			volumes, err := tx.GetStorageVolumes(ctx, true, db.StorageVolumeFilter{PoolID: &poolID})
			if err != nil {
				return fmt.Errorf("Failed loading storage volumes: %w", err)
			}

			for _, vol := range volumes {
				if vol.Type != dbCluster.StoragePoolVolumeTypeNameImage {
					return fmt.Errorf("Volume %q of type %q in project %q still exists in storage pool %q", vol.Name, vol.Type, vol.Project, pool.Name())
				}

				removeImgFingerprints = append(removeImgFingerprints, vol.Name)
			}

			return nil
		})
		if err != nil {
			return err
		}

		for _, removeImgFingerprint := range removeImgFingerprints {
			err = pool.DeleteImage(removeImgFingerprint, nil)
			if err != nil {
				return fmt.Errorf("Error deleting image %q from storage pool %q: %w", removeImgFingerprint, pool.Name(), err)
			}
		}
	}

	if pool.LocalStatus() != api.StoragePoolStatusPending {
		err = pool.Delete(clientType, nil)
		if err != nil {
			return err
		}
	}

	// If this is a cluster notification, we're done, any database work will be done by the node that is
	// originally serving the request.
	if isClusterNotification {
		return nil
	}

	// If we are clustered, also notify all other nodes.
	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		return client.DeleteStoragePool(pool.Name())
	})
	if err != nil {
		return err
	}

	err = dbStoragePoolDeleteAndUpdateCache(s, pool.Name())
	if err != nil {
		return err
	}

	requestor := request.CreateRequestor(reqContext)
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.StoragePoolDeleted.Event(pool.Name(), requestor, nil))

	return nil
}
