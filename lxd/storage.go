package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// Simply cache used to storage the activated drivers on this LXD instance. This
// allows us to avoid querying the database everytime and API call is made.
var storagePoolUsedDriversCacheVal atomic.Value
var storagePoolSupportedDriversCacheVal atomic.Value
var storagePoolDriversCacheLock sync.Mutex

// readStoragePoolDriversCache returns supported and used storage driver info.
func readStoragePoolDriversCache() ([]api.ServerStorageDriverInfo, map[string]string) {
	usedDrivers := storagePoolUsedDriversCacheVal.Load()
	if usedDrivers == nil {
		usedDrivers = map[string]string{}
	}

	supportedDrivers := storagePoolSupportedDriversCacheVal.Load()
	if supportedDrivers == nil {
		supportedDrivers = []api.ServerStorageDriverInfo{}
	}

	driverInfo, _ := supportedDrivers.([]api.ServerStorageDriverInfo)
	driversUsed, _ := usedDrivers.(map[string]string)
	return driverInfo, driversUsed
}

func storageStartup(s *state.State) error {
	// Update the storage drivers supported and used cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s.ShutdownCtx, s)

	var poolNames []string

	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		poolNames, err = tx.GetCreatedStoragePoolNames(ctx)

		return err
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			logger.Debug("No existing storage pools detected")

			// There aren't any storage pools.
			// Unblock all the waitready callers using the --storage flag.
			s.StorageReady.Cancel()

			return nil
		}

		return fmt.Errorf("Failed loading existing storage pools: %w", err)
	}

	initPools := make(map[string]struct{}, len(poolNames))
	for _, poolName := range poolNames {
		initPools[poolName] = struct{}{}
	}

	initPool := func(poolName string) bool {
		logger.Debug("Initializing storage pool", logger.Ctx{"pool": poolName})

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			if response.IsNotFoundError(err) {
				return true // Nothing to activate as pool has been deleted.
			}

			logger.Error("Failed loading storage pool", logger.Ctx{"pool": poolName, "err": err})

			return false
		}

		_, err = pool.Mount()
		if err != nil {
			logger.Error("Failed mounting storage pool", logger.Ctx{"pool": poolName, "err": err})
			_ = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpsertWarningLocalNode(ctx, "", entity.TypeStoragePool, int(pool.ID()), warningtype.StoragePoolUnvailable, err.Error())
			})

			return false
		}

		logger.Info("Initialized storage pool", logger.Ctx{"pool": poolName})
		_ = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.DB.Cluster, "", warningtype.StoragePoolUnvailable, entity.TypeStoragePool, int(pool.ID()))

		return true
	}

	// Try initializing storage pools in random order.
	for poolName := range initPools {
		if initPool(poolName) {
			// Storage pool initialized successfully so remove it from the list so its not retried.
			delete(initPools, poolName)
		}
	}

	// For any remaining storage pools that were not successfully initialised, we now start a go routine to
	// periodically try to initialize them again in the background.
	if len(initPools) > 0 {
		go func() {
			delay := 5 * time.Second // first delay
			step := 5 * time.Second  // increment per iteration
			maxDelay := time.Minute  // cap at 60s

			for {
				t := time.NewTimer(delay)

				select {
				case <-s.ShutdownCtx.Done():
					t.Stop()
					return
				case <-t.C:
					t.Stop()

					// Try initializing remaining storage pools in random order.
					tryInstancesStart := false
					for poolName := range initPools {
						if initPool(poolName) {
							// Storage pool initialized successfully or deleted so
							// remove it from the list so its not retried.
							delete(initPools, poolName)
							tryInstancesStart = true
						}
					}

					if len(initPools) <= 0 {
						logger.Info("All storage pools initialized")
					}

					// At least one remaining storage pool was initialized, check if any
					// instances can now start.
					if tryInstancesStart {
						instances, err := instance.LoadNodeAll(s, instancetype.Any)
						if err != nil {
							logger.Error("Failed loading instances to start", logger.Ctx{"err": err})
						} else {
							instancesStart(s, instances)
						}
					}

					if len(initPools) <= 0 {
						// All storage pools are ready now after performing some retries.
						// This unblocks any waitready caller using the --storage flag.
						s.StorageReady.Cancel()

						return // Our job here is done.
					}
				}

				if delay < maxDelay {
					delay += step
					if delay > maxDelay {
						delay = maxDelay
					}
				}
			}
		}()
	} else {
		// All storage pools are ready.
		// This unblocks any waitready caller using the --storage flag.
		s.StorageReady.Cancel()

		logger.Info("All storage pools initialized")
	}

	return nil
}

func storageStop(s *state.State) {
	if s.DB.Cluster == nil {
		logger.Warn("Skipping storage stop due to global database not being available")
		return
	}

	logger.Info("Stopping storage pools")

	var err error
	var pools []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		pools, err = tx.GetStoragePoolNames(ctx)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		logger.Error("Failed to get storage pools", logger.Ctx{"err": err})
	}

	for _, poolName := range pools {
		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			logger.Error("Failed to get storage pool", logger.Ctx{"pool": poolName, "err": err})
			continue
		}

		_, err = pool.Unmount()
		if err != nil {
			logger.Error("Unable to unmount storage pool", logger.Ctx{"pool": poolName, "err": err})
			continue
		}
	}
}

func storagePoolDriversCacheUpdate(ctx context.Context, s *state.State) {
	// Get a list of all storage drivers currently in use
	// on this LXD instance. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)

	var drivers []string

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		drivers, err = tx.GetStorageDrivers(ctx)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		return
	}

	usedDrivers := map[string]string{}

	// Get the driver info.
	info := storageDrivers.SupportedDrivers(s)
	supportedDrivers := make([]api.ServerStorageDriverInfo, 0, len(info))

	for _, entry := range info {
		supportedDrivers = append(supportedDrivers, api.ServerStorageDriverInfo{
			Name:    entry.Name,
			Version: entry.Version,
			Remote:  entry.Remote,
		})

		if slices.Contains(drivers, entry.Name) {
			usedDrivers[entry.Name] = entry.Version
		}
	}

	// Prepare the cache entries.
	backends := []string{}
	for k, v := range usedDrivers {
		backends = append(backends, k+" "+v)
	}

	// Update the user agent.
	version.UserAgentStorageBackends(backends)

	storagePoolDriversCacheLock.Lock()
	storagePoolUsedDriversCacheVal.Store(usedDrivers)
	storagePoolSupportedDriversCacheVal.Store(supportedDrivers)
	storagePoolDriversCacheLock.Unlock()
}
