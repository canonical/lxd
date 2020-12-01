package main

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func storagePoolUpdate(state *state.State, name, newDescription string, newConfig map[string]string, withDB bool) error {
	pool, err := storagePools.GetPoolByName(state, name)
	if err != nil {
		return err
	}

	return pool.Update(!withDB, newDescription, newConfig, nil)
}

// storagePoolDBCreate creates a storage pool DB entry and returns the created Pool ID.
func storagePoolDBCreate(s *state.State, poolName, poolDescription string, driver string, config map[string]string) (int64, error) {
	// Check that the storage pool does not already exist.
	_, err := s.Cluster.GetStoragePoolID(poolName)
	if err == nil {
		return -1, fmt.Errorf("The storage pool already exists")
	}

	// Make sure that we don't pass a nil to the next function.
	if config == nil {
		config = map[string]string{}
	}
	err = storagePoolValidate(poolName, driver, config)
	if err != nil {
		return -1, err
	}

	// Fill in the defaults.
	err = storagePoolFillDefault(poolName, driver, config)
	if err != nil {
		return -1, err
	}

	// Create the database entry for the storage pool.
	id, err := dbStoragePoolCreateAndUpdateCache(s, poolName, poolDescription, driver, config)
	if err != nil {
		return -1, fmt.Errorf("Error inserting %s into database: %s", poolName, err)
	}

	return id, nil
}

func storagePoolValidate(poolName string, driverName string, config map[string]string) error {
	// Check if the storage pool name is valid.
	err := storagePools.ValidName(poolName)
	if err != nil {
		return err
	}

	// Validate the requested storage pool configuration.
	err = storagePoolValidateConfig(poolName, driverName, config, nil)
	if err != nil {
		return err
	}

	return nil
}

func storagePoolCreateGlobal(state *state.State, req api.StoragePoolsPost) error {
	// Create the database entry.
	id, err := storagePoolDBCreate(state, req.Name, req.Description, req.Driver, req.Config)
	if err != nil {
		return err
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path. Track whether or not we want to undo the changes
	// using a closure.
	tryUndo := true
	defer func() {
		if !tryUndo {
			return
		}

		dbStoragePoolDeleteAndUpdateCache(state, req.Name)
	}()

	_, err = storagePoolCreateLocal(state, id, req, false)
	if err != nil {
		return err
	}

	tryUndo = false
	return nil
}

// This performs local pool setup and updates DB record if config was changed during pool setup.
func storagePoolCreateLocal(state *state.State, id int64, req api.StoragePoolsPost, isNotification bool) (map[string]string, error) {
	tryUndo := true

	// Make a copy of the req for later diff.
	var updatedConfig map[string]string
	var updatedReq api.StoragePoolsPost
	shared.DeepCopy(&req, &updatedReq)

	// Fill in the defaults.
	err := storagePoolFillDefault(updatedReq.Name, updatedReq.Driver, updatedReq.Config)
	if err != nil {
		return nil, err
	}

	// Create the pool.
	pool, err := storagePools.CreatePool(state, id, &updatedReq, isNotification, nil)
	if err != nil {
		return nil, err
	}

	// Mount the pool.
	_, err = pool.Mount()
	if err != nil {
		return nil, err
	}

	// Record the updated config.
	updatedConfig = updatedReq.Config

	// Setup revert function.
	defer func() {
		if !tryUndo {
			return
		}

		pool.Delete(isNotification, nil)
	}()

	// In case the storage pool config was changed during the pool creation,
	// we need to update the database to reflect this change. This can e.g.
	// happen, when we create a loop file image. This means we append ".img"
	// to the path the user gave us and update the config in the storage
	// callback. So diff the config here to see if something like this has
	// happened.
	configDiff, _ := storagePools.ConfigDiff(req.Config, updatedConfig)
	if len(configDiff) > 0 {
		// Create the database entry for the storage pool.
		err = state.Cluster.UpdateStoragePool(req.Name, req.Description, updatedConfig)
		if err != nil {
			return nil, errors.Wrapf(err, "Error updating storage pool config after local create for %q", req.Name)
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	tryUndo = false

	return updatedConfig, nil
}

// Helper around the low-level DB API, which also updates the driver names cache.
func dbStoragePoolCreateAndUpdateCache(s *state.State, poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	id, err := s.Cluster.CreateStoragePool(poolName, poolDescription, poolDriver, poolConfig)
	if err != nil {
		return id, err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s)

	return id, nil
}

// Helper around the low-level DB API, which also updates the driver names
// cache.
func dbStoragePoolDeleteAndUpdateCache(s *state.State, poolName string) error {
	_, err := s.Cluster.RemoveStoragePool(poolName)
	if err != nil {
		return err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s)

	return err
}
