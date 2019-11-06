package storage

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
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

		// It is possible for the project name to be encoded into the volume name in the
		// format <project>_<volume>. However not all volume types currently use this
		// encoding format, so if there is no underscore in the volume name then we assume
		// the project is default.
		project := "default"

		// Currently only Containers and VMs support project level volumes.
		// This means that other volume types may have underscores in their names that don't
		// indicate the project name.
		if volType == drivers.VolumeTypeContainer || volType == drivers.VolumeTypeVM {
			volParts := strings.SplitN(volName, "_", 2)
			if len(volParts) > 1 {
				project = volParts[0]
				volName = volParts[1]
			}
		}

		volID, _, err := state.Cluster.StoragePoolNodeVolumeGetTypeByProject(project, volName, volTypeID, poolID)
		if err != nil {
			if err == db.ErrNoSuchObject {
				return -1, fmt.Errorf("Failed to get volume ID for project '%s', volume '%s', type '%s': Volume doesn't exist", project, volName, volType)
			}

			return -1, err
		}

		return volID, nil
	}
}

// CreatePool creates a new storage pool on disk and returns a Pool interface.
// If the pool's driver is not recognised then drivers.ErrUnknownDriver is returned.
func CreatePool(state *state.State, poolID int64, dbPool *api.StoragePoolsPost, localOnly bool, op *operations.Operation) (Pool, error) {
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
		pool.logger = logging.AddContext(logger.Log, log.Ctx{"driver": "mock", "pool": pool.name})
		return &pool, nil
	}

	logger := logging.AddContext(logger.Log, log.Ctx{"driver": dbPool.Driver, "pool": dbPool.Name})

	// Load the storage driver.
	driver, err := drivers.Load(state, dbPool.Driver, dbPool.Name, dbPool.Config, logger, volIDFuncMake(state, poolID), validateVolumeCommonRules)
	if err != nil {
		return nil, err
	}

	// Setup the pool struct.
	pool := lxdBackend{}
	pool.driver = driver
	pool.id = poolID
	pool.name = dbPool.Name
	pool.state = state
	pool.logger = logger

	// Create the pool itself on the storage device..
	err = pool.create(dbPool, localOnly, op)
	if err != nil {
		return nil, err
	}

	return &pool, nil
}

// GetPoolByName retrieves the pool from the database by its name and returns a Pool interface.
// If the pool's driver is not recognised then drivers.ErrUnknownDriver is returned.
func GetPoolByName(state *state.State, name string) (Pool, error) {
	// Handle mock requests.
	if MockBackend {
		pool := mockBackend{}
		pool.name = name
		pool.state = state
		pool.logger = logging.AddContext(logger.Log, log.Ctx{"driver": "mock", "pool": pool.name})
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

	logger := logging.AddContext(logger.Log, log.Ctx{"driver": dbPool.Driver, "pool": dbPool.Name})

	// Load the storage driver.
	driver, err := drivers.Load(state, dbPool.Driver, dbPool.Name, dbPool.Config, logger, volIDFuncMake(state, poolID), validateVolumeCommonRules)
	if err != nil {
		return nil, err
	}

	// Setup the pool struct.
	pool := lxdBackend{}
	pool.driver = driver
	pool.id = poolID
	pool.name = dbPool.Name
	pool.state = state
	pool.logger = logger

	return &pool, nil
}

// GetPoolByInstance retrieves the pool from the database using the instance's pool.
// If the pool's driver is not recognised then drivers.ErrUnknownDriver is returned. If the pool's
// driver does not support the instance's type then drivers.ErrNotImplemented is returned.
func GetPoolByInstance(s *state.State, inst Instance) (Pool, error) {
	poolName, err := s.Cluster.ContainerPool(inst.Project(), inst.Name())
	if err != nil {
		return nil, err
	}

	pool, err := GetPoolByName(s, poolName)
	if err != nil {
		return nil, err
	}

	volType, err := InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return nil, err
	}

	for _, supportedType := range pool.Driver().Info().VolumeTypes {
		if supportedType == volType {
			return pool, nil
		}
	}

	// Return drivers not implemented error for consistency with predefined errors returned by
	// GetPoolByName (which can return drivers.ErrUnknownDriver).
	return nil, drivers.ErrNotImplemented
}
