package storage

import (
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// MockBackend controls whether to run the storage logic in mock mode.
var MockBackend = false

// CreatePool creates a new storage pool on disk and returns a Pool interface.
func CreatePool(state *state.State, id int64, dbPool *api.StoragePool, op *operations.Operation) (Pool, error) {
	// Sanity checks
	if dbPool == nil {
		return nil, ErrNilValue
	}

	// Ensure a config map exists
	if dbPool.Config == nil {
		dbPool.Config = map[string]string{}
	}

	// Handle mock requests
	if MockBackend {
		pool := mockBackend{}
		pool.name = dbPool.Name
		pool.state = state
		return &pool, nil
	}

	// Setup the pool struct
	pool := lxdBackend{}
	pool.id = id
	pool.name = dbPool.Name
	pool.state = state

	// Create the pool itself (also responsible for setting driver)
	err := pool.create(dbPool, op)
	if err != nil {
		return nil, err
	}

	return &pool, nil
}

// GetPoolByName retrieves the pool from the database by its name and returns a Pool interface.
func GetPoolByName(state *state.State, name string) (Pool, error) {
	// Handle mock requests
	if MockBackend {
		pool := mockBackend{}
		pool.name = name
		pool.state = state
		return &pool, nil
	}

	// Load the database record
	id, dbPool, err := state.Cluster.StoragePoolGet(name)
	if err != nil {
		return nil, err
	}

	// Ensure a config map exists
	if dbPool.Config == nil {
		dbPool.Config = map[string]string{}
	}

	// Load the storage driver
	path := shared.VarPath("storage-pools", name)
	driver, err := drivers.Load(dbPool.Driver, dbPool.Name, path, dbPool.Config)
	if err != nil {
		return nil, err
	}

	// Setup the pool struct
	pool := lxdBackend{}
	pool.driver = driver
	pool.id = id
	pool.name = dbPool.Name
	pool.state = state

	return &pool, nil
}
