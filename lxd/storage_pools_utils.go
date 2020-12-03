package main

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

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

func storagePoolCreateGlobal(state *state.State, req api.StoragePoolsPost, clientType request.ClientType) error {
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

	_, err = storagePoolCreateLocal(state, id, req, clientType)
	if err != nil {
		return err
	}

	tryUndo = false
	return nil
}

// This performs local pool setup and updates DB record if config was changed during pool setup.
// Returns resulting config.
func storagePoolCreateLocal(state *state.State, id int64, req api.StoragePoolsPost, clientType request.ClientType) (map[string]string, error) {
	// Setup revert.
	revert := revert.New()
	defer revert.Fail()

	// Make a copy of the req for later diff.
	var updatedReq api.StoragePoolsPost
	shared.DeepCopy(&req, &updatedReq)

	// Fill in the node specific defaults.
	err := storagePoolFillDefault(updatedReq.Name, updatedReq.Driver, updatedReq.Config)
	if err != nil {
		return nil, err
	}

	configDiff, _ := storagePools.ConfigDiff(req.Config, updatedReq.Config)
	if len(configDiff) > 0 {
		// Update the database entry for the storage pool.
		err = state.Cluster.UpdateStoragePool(req.Name, req.Description, updatedReq.Config)
		if err != nil {
			return nil, errors.Wrapf(err, "Error updating storage pool config after local fill defaults for %q", req.Name)
		}
	}

	// Load pool record.
	pool, err := storagePools.GetPoolByName(state, updatedReq.Name)
	if err != nil {
		return nil, err
	}

	if pool.LocalStatus() == api.NetworkStatusCreated {
		logger.Debug("Skipping storage pool create as already created locally", log.Ctx{"pool": pool.Name()})

		return pool.Driver().Config(), nil
	}

	// Create the pool.
	err = pool.Create(clientType, nil)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { pool.Delete(clientType, nil) })

	// Mount the pool.
	_, err = pool.Mount()
	if err != nil {
		return nil, err
	}

	// In case the storage pool config was changed during the pool creation, we need to update the database to
	// reflect this change. This can e.g. happen, when we create a loop file image. This means we append ".img"
	// to the path the user gave us and update the config in the storage callback. So diff the config here to
	// see if something like this has happened.
	configDiff, _ = storagePools.ConfigDiff(updatedReq.Config, pool.Driver().Config())
	if len(configDiff) > 0 {
		// Update the database entry for the storage pool.
		err = state.Cluster.UpdateStoragePool(req.Name, req.Description, pool.Driver().Config())
		if err != nil {
			return nil, errors.Wrapf(err, "Error updating storage pool config after local create for %q", req.Name)
		}
	}

	// Set storage pool node to storagePoolCreated.
	err = state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.StoragePoolNodeCreated(id)
	})
	if err != nil {
		return nil, err
	}
	logger.Debug("Marked storage pool local status as created", log.Ctx{"pool": req.Name})

	revert.Success()
	return pool.Driver().Config(), nil
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
