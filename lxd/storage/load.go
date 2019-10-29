package storage

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
)

// MockBackend controls whether to run the storage logic in mock mode.
var MockBackend = false

// volIDFuncMake returns a function that can be supplied to the underlying storage drivers allowing
// them to lookup the volume ID for a specific volume type and volume name. This function is tied
// to the Pool ID that it is generated for, meaning the storage drivers do not need to know the ID
// of the pool they belong to, or do they need access to the database.
func volIDFuncMake(state *state.State, poolID int64) func(volType drivers.VolumeType, volName string) (int64, error) {
	// Return a function to retrieve a volume ID for a volume Name for use in driver.
	return func(volType drivers.VolumeType, volName string) (int64, error) {
		volTypeID, err := VolumeTypeToDBType(volType)
		if err != nil {
			return -1, err
		}

		// TODO add project support in the future by splitting the volName by "_".
		volID, _, err := state.Cluster.StoragePoolNodeVolumeGetTypeByProject("default", volName, volTypeID, poolID)
		if err != nil {
			if err == db.ErrNoSuchObject {
				return -1, fmt.Errorf("Volume doesn't exist")
			}

			return -1, err
		}

		return volID, nil
	}
}

// CreatePool creates a new storage pool on disk and returns a Pool interface.
func CreatePool(state *state.State, poolID int64, dbPool *api.StoragePool, op *operations.Operation) (Pool, error) {
	// Sanity checks.
	if dbPool == nil {
		return nil, ErrNilValue
	}

	// Ensure a config map exists.
	if dbPool.Config == nil {
		dbPool.Config = map[string]string{}
	}

	// Handle mock requests.
	if MockBackend {
		pool := mockBackend{}
		pool.name = dbPool.Name
		pool.state = state
		return &pool, nil
	}

	// Load the storage driver.
	driver, err := drivers.Load(state, dbPool.Driver, dbPool.Name, dbPool.Config, volIDFuncMake(state, poolID), validateVolumeCommonRules)
	if err != nil {
		return nil, err
	}

	// Setup the pool struct.
	pool := lxdBackend{}
	pool.driver = driver
	pool.id = poolID
	pool.name = dbPool.Name
	pool.state = state

	// Create the pool itself on the storage device..
	err = pool.create(dbPool, op)
	if err != nil {
		return nil, err
	}

	return &pool, nil
}

// GetPoolByName retrieves the pool from the database by its name and returns a Pool interface.
func GetPoolByName(state *state.State, name string) (Pool, error) {
	// Handle mock requests.
	if MockBackend {
		pool := mockBackend{}
		pool.name = name
		pool.state = state
		return &pool, nil
	}

	// Load the database record.
	poolID, dbPool, err := state.Cluster.StoragePoolGet(name)
	if err != nil {
		return nil, err
	}

	// Ensure a config map exists.
	if dbPool.Config == nil {
		dbPool.Config = map[string]string{}
	}

	// Load the storage driver.
	driver, err := drivers.Load(state, dbPool.Driver, dbPool.Name, dbPool.Config, volIDFuncMake(state, poolID), validateVolumeCommonRules)
	if err != nil {
		return nil, err
	}

	// Setup the pool struct.
	pool := lxdBackend{}
	pool.driver = driver
	pool.id = poolID
	pool.name = dbPool.Name
	pool.state = state

	return &pool, nil
}
