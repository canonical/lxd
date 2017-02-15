package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
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

	pools, err := dbStoragePools(d.db)
	if err != nil && err != NoSuchObjectError {
		return InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.StoragePool{}
	for _, pool := range pools {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, pool))
		} else {
			plID, pl, err := dbStoragePoolGet(d.db, pool)
			if err != nil {
				continue
			}

			// Get all users of the storage pool.
			poolUsedBy, err := storagePoolUsedByGet(d.db, plID, pool)
			if err != nil && err != NoSuchObjectError {
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
	req := api.StoragePool{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if req.Driver == "" {
		return BadRequest(fmt.Errorf("No driver provided"))
	}

	// Check if the storage pool name is valid.
	err = storageValidName(req.Name)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the storage pool does not already exist.
	_, err = dbStoragePoolGetID(d.db, req.Name)
	if err == nil {
		return BadRequest(fmt.Errorf("The storage pool already exists"))
	}

	// Make sure that we don't pass a nil to the next function.
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Validate the requested storage pool configuration.
	err = storagePoolValidateConfig(req.Name, req.Driver, req.Config)
	if err != nil {
		return BadRequest(err)
	}

	// Create the database entry for the storage pool.
	_, err = dbStoragePoolCreate(d.db, req.Name, req.Driver, req.Config)
	if err != nil {
		return InternalError(fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path. Track whether or not we want to undo the changes
	// using a closure.
	tryUndo := true
	defer func() {
		if tryUndo {
			dbStoragePoolDelete(d.db, req.Name)
		}
	}()

	s, err := storagePoolInit(d, req.Name)
	if err != nil {
		return InternalError(err)
	}

	err = s.StoragePoolCreate()
	if err != nil {
		return InternalError(err)
	}
	defer func() {
		if tryUndo {
			s.StoragePoolDelete()
		}
	}()

	// In case the storage pool config was changed during the pool creation,
	// we need to update the database to reflect this change. This can e.g.
	// happen, when we create a loop file image. This means we append ".img"
	// to the path the user gave us and update the config in the storage
	// callback. So diff the config here to see if something like this has
	// happened.
	postCreateConfig := s.GetStoragePoolWritable().Config
	configDiff, _ := storageConfigDiff(req.Config, postCreateConfig)
	if len(configDiff) > 0 {
		// Create the database entry for the storage pool.
		err = dbStoragePoolUpdate(d.db, req.Name, postCreateConfig)
		if err != nil {
			return InternalError(fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	tryUndo = false

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, req.Name))
}

var storagePoolsCmd = Command{name: "storage-pools", get: storagePoolsGet, post: storagePoolsPost}

// /1.0/storage-pools/{name}
// Get a single storage pool.
func storagePoolGet(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
	poolID, pool, err := dbStoragePoolGet(d.db, poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get all users of the storage pool.
	poolUsedBy, err := storagePoolUsedByGet(d.db, poolID, poolName)
	if err != nil && err != NoSuchObjectError {
		return SmartError(err)
	}
	pool.UsedBy = poolUsedBy

	etag := []interface{}{pool.Name, pool.UsedBy, pool.Config}

	return SyncResponseETag(true, &pool, etag)
}

// /1.0/storage-pools/{name}
// Replace pool properties.
func storagePoolPut(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
	_, dbInfo, err := dbStoragePoolGet(d.db, poolName)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.UsedBy, dbInfo.Config}

	err = etagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StoragePool{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Validate the configuration
	err = storagePoolValidateConfig(poolName, req.Driver, req.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolUpdate(d, poolName, req.Config)
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
	_, dbInfo, err := dbStoragePoolGet(d.db, poolName)
	if dbInfo != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.UsedBy, dbInfo.Config}

	err = etagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StoragePool{}
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
	err = storagePoolValidateConfig(poolName, req.Driver, req.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolUpdate(d, poolName, req.Config)
	if err != nil {
		return InternalError(fmt.Errorf("Failed to update the storage pool configuration."))
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{name}
// Delete storage pool.
func storagePoolDelete(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	poolID, err := dbStoragePoolGetID(d.db, poolName)
	if err != nil {
		return NotFound
	}

	// Check if the storage pool has any volumes associated with it, if so
	// error out.
	volumeCount, err := dbStoragePoolVolumesGetNames(d.db, poolID)
	if volumeCount > 0 {
		return BadRequest(fmt.Errorf("Storage pool \"%s\" has volumes attached to it.", poolName))
	}

	s, err := storagePoolInit(d, poolName)
	if err != nil {
		return InternalError(err)
	}

	err = s.StoragePoolDelete()
	if err != nil {
		return InternalError(err)
	}

	err = dbStoragePoolDelete(d.db, poolName)
	if err != nil {
		return InternalError(err)
	}

	// In case we deleted the default storage pool, try to update the
	// default profile.
	defaultID, defaultProfile, err := dbProfileGet(d.db, "default")
	if err != nil {
		return EmptySyncResponse
	}
	for k, v := range defaultProfile.Devices {
		if v["type"] == "disk" && v["path"] == "/" {
			if v["pool"] == poolName {
				defaultProfile.Devices[k]["pool"] = ""

				tx, err := dbBegin(d.db)
				if err != nil {
					return EmptySyncResponse
				}

				err = dbProfileConfigClear(tx, defaultID)
				if err != nil {
					tx.Rollback()
					return EmptySyncResponse
				}

				err = dbProfileConfigAdd(tx, defaultID, defaultProfile.Config)
				if err != nil {
					tx.Rollback()
					return EmptySyncResponse
				}

				err = dbDevicesAdd(tx, "profile", defaultID, defaultProfile.Devices)
				if err != nil {
					tx.Rollback()
					return EmptySyncResponse
				}

				err = txCommit(tx)
				if err != nil {
					return EmptySyncResponse
				}
			}
		}
	}

	return EmptySyncResponse
}

var storagePoolCmd = Command{name: "storage-pools/{name}", get: storagePoolGet, put: storagePoolPut, patch: storagePoolPatch, delete: storagePoolDelete}
