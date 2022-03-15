package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/warnings"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
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

	return supportedDrivers.([]api.ServerStorageDriverInfo), usedDrivers.(map[string]string)
}

func resetContainerDiskIdmap(container instance.Container, srcIdmap *idmap.IdmapSet) error {
	dstIdmap, err := container.DiskIdmap()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(idmap.IdmapSet)
	}

	if !srcIdmap.Equals(dstIdmap) {
		var jsonIdmap string
		if srcIdmap != nil {
			idmapBytes, err := json.Marshal(srcIdmap.Idmap)
			if err != nil {
				return err
			}
			jsonIdmap = string(idmapBytes)
		} else {
			jsonIdmap = "[]"
		}

		logger.Debug("Setting new volatile.last_state.idmap from source instance", log.Ctx{"project": container.Project(), "instance": container.Name(), "sourceIdmap": srcIdmap})
		err := container.VolatileSet(map[string]string{"volatile.last_state.idmap": jsonIdmap})
		if err != nil {
			return err
		}
	}

	return nil
}

func storageStartup(s *state.State, forceCheck bool) error {
	// Update the storage drivers supported and used cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s)

	poolNames, err := s.Cluster.GetCreatedStoragePoolNames()
	if err != nil {
		if errors.Is(err, db.ErrNoSuchObject) {
			logger.Debug("No existing storage pools detected")
			return nil
		}

		return fmt.Errorf("Failed loading existing storage pools: %w", err)
	}

	initPools := make(map[string]struct{}, len(poolNames))
	for _, poolName := range poolNames {
		initPools[poolName] = struct{}{}
	}

	// In case the daemon got killed during upgrade we will already have a
	// valid storage pool entry but it might have gotten messed up and so we
	// cannot perform StoragePoolCheck(). This case can be detected by
	// looking at the patches db: If we already have a storage pool defined
	// but the upgrade somehow got messed up then there will be no
	// "storage_api" entry in the db.
	if len(initPools) > 0 && !forceCheck {
		appliedPatches, err := s.Node.GetAppliedPatches()
		if err != nil {
			return err
		}

		if !shared.StringInSlice("storage_api", appliedPatches) {
			logger.Warn(`Incorrectly applied "storage_api" patch, skipping storage pool initialization as it might be corrupt`)
			return nil
		}
	}

	initPool := func(poolName string) bool {
		logger.Debug("Initializing storage pool", log.Ctx{"pool": poolName})

		pool, err := storagePools.GetPoolByName(s, poolName)
		if err != nil {
			if errors.Is(err, db.ErrNoSuchObject) {
				return true // Nothing to activate as pool has been deleted.
			}

			logger.Warn("Failed loading storage pool", log.Ctx{"pool": poolName, "err": err})

			return false
		}

		_, err = pool.Mount()
		if err != nil {
			logger.Warn("Failed mounting storage pool", log.Ctx{"pool": poolName, "err": err})
			s.Cluster.UpsertWarningLocalNode("", cluster.TypeStoragePool, int(pool.ID()), db.WarningStoragePoolUnvailable, err.Error())

			return false
		}

		logger.Info("Initialized storage pool", log.Ctx{"pool": poolName})
		warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.Cluster, "", db.WarningStoragePoolUnvailable, cluster.TypeStoragePool, int(pool.ID()))

		return true
	}

	// Try initializing storage pools in random order.
	for poolName := range initPools {
		if initPool(poolName) {
			// Storage pool initialized successfully then remove it from the list so its not retried.
			delete(initPools, poolName)
		}
	}

	// For any remaining storage pools that were not successfully initialised, we now start a go routine to
	// periodically try to initialize them again in the background.
	if len(initPools) > 0 {
		go func() {
			for {
				t := time.NewTimer(time.Duration(time.Minute))

				select {
				case <-s.Context.Done():
					t.Stop()
					return
				case <-t.C:
					t.Stop()

					// Try initializing remaining storage pools in random order.
					tryInstancesStart := false
					for poolName := range initPools {
						if initPool(poolName) {
							// Storage pool initialized successfully then remove it
							// from the list so its not retried.
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
							logger.Warn("Failed loading instances to start", log.Ctx{"err": err})
						} else {
							instancesStart(s, instances)
						}
					}

					if len(initPools) <= 0 {
						return // Our job here is done.
					}
				}
			}
		}()
	} else {
		logger.Info("All storage pools initialized")
	}

	return nil
}

func storagePoolDriversCacheUpdate(s *state.State) {
	// Get a list of all storage drivers currently in use
	// on this LXD instance. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)
	drivers, err := s.Cluster.GetStoragePoolDrivers()
	if err != nil && err != db.ErrNoSuchObject {
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

		if shared.StringInSlice(entry.Name, drivers) {
			usedDrivers[entry.Name] = entry.Version
		}
	}

	// Prepare the cache entries.
	backends := []string{}
	for k, v := range usedDrivers {
		backends = append(backends, fmt.Sprintf("%s %s", k, v))
	}

	// Update the user agent.
	version.UserAgentStorageBackends(backends)

	storagePoolDriversCacheLock.Lock()
	storagePoolUsedDriversCacheVal.Store(usedDrivers)
	storagePoolSupportedDriversCacheVal.Store(supportedDrivers)
	storagePoolDriversCacheLock.Unlock()

	return
}
