package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// /1.0/storage-pools
// List all storage pools.
func storagePoolsGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	pools, err := d.db.StoragePools()
	if err != nil && err != db.NoSuchObjectError {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []api.StoragePool{}
	for _, pool := range pools {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, pool))
		} else {
			plID, pl, err := d.db.StoragePoolGet(pool)
			if err != nil {
				continue
			}

			// Get all users of the storage pool.
			poolUsedBy, err := storagePoolUsedByGet(d.db, plID, pool)
			if err != nil {
				return SmartError(err)
			}
			pl.UsedBy = poolUsedBy

			resultMap = append(resultMap, *pl)
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

// /1.0/storage-pools
// Create a storage pool.
func storagePoolsPost(d *Daemon, r *http.Request) Response {
	req := api.StoragePoolsPost{}

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
		return BadRequest(fmt.Errorf("Storage pool names may not contain slashes"))
	}

	if req.Driver == "" {
		return BadRequest(fmt.Errorf("No driver provided"))
	}

	err = storagePoolCreateInternal(d.State(), req.Name, req.Description, req.Driver, req.Config)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, req.Name))
}

var storagePoolsCmd = Command{name: "storage-pools", get: storagePoolsGet, post: storagePoolsPost}

// /1.0/storage-pools/{name}
// Get a single storage pool.
func storagePoolGet(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
	poolID, pool, err := d.db.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get all users of the storage pool.
	poolUsedBy, err := storagePoolUsedByGet(d.db, poolID, poolName)
	if err != nil && err != db.NoSuchObjectError {
		return SmartError(err)
	}
	pool.UsedBy = poolUsedBy

	etag := []interface{}{pool.Name, pool.Driver, pool.Config}

	return SyncResponseETag(true, &pool, etag)
}

// /1.0/storage-pools/{name}
// Replace pool properties.
func storagePoolPut(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
	_, dbInfo, err := d.db.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Driver, dbInfo.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StoragePoolPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Validate the configuration
	err = storagePoolValidateConfig(poolName, dbInfo.Driver, req.Config, dbInfo.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolUpdate(d.State(), poolName, req.Description, req.Config)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{name}
// Change pool properties.
func storagePoolPatch(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, err := d.db.StoragePoolGet(poolName)
	if dbInfo != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Driver, dbInfo.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StoragePoolPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Config stacking
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range dbInfo.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Validate the configuration
	err = storagePoolValidateConfig(poolName, dbInfo.Driver, req.Config, dbInfo.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolUpdate(d.State(), poolName, req.Description, req.Config)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{name}
// Delete storage pool.
func storagePoolDelete(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	poolID, err := d.db.StoragePoolGetID(poolName)
	if err != nil {
		return NotFound
	}

	// Check if the storage pool has any volumes associated with it, if so
	// error out.
	volumeCount, err := d.db.StoragePoolVolumesGetNames(poolID)
	if err != nil {
		return InternalError(err)
	}

	if volumeCount > 0 {
		return BadRequest(fmt.Errorf("storage pool \"%s\" has volumes attached to it", poolName))
	}

	// Check if the storage pool is still referenced in any profiles.
	profiles, err := profilesUsingPoolGetNames(d.db, poolName)
	if err != nil {
		return SmartError(err)
	}

	if len(profiles) > 0 {
		return BadRequest(fmt.Errorf("Storage pool \"%s\" has profiles using it:\n%s", poolName, strings.Join(profiles, "\n")))
	}

	s, err := storagePoolInit(d.State(), poolName)
	if err != nil {
		return InternalError(err)
	}

	err = s.StoragePoolDelete()
	if err != nil {
		return InternalError(err)
	}

	err = dbStoragePoolDeleteAndUpdateCache(d.db, poolName)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var storagePoolCmd = Command{name: "storage-pools/{name}", get: storagePoolGet, put: storagePoolPut, patch: storagePoolPatch, delete: storagePoolDelete}
