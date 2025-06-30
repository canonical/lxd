package storage

import (
	"fmt"

	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
)

// backupConfigCache is used to cache pools and volumes during backup config creation.
type backupConfigCache struct {
	pools map[string]Pool
	// The volume cache is using the project as its first dimension.
	// By default all projects use features.storage.volumes=true which uses the volumes from the default project.
	// In this case the top level dimension only ever has one entry (default project) which causes the cache to stay small.
	// Having the pool as the top level dimension would cause a default project entry under every pool from which volumes are consumed.
	volumes map[string]map[string]map[string]*backupConfig.Volume
	state   *state.State
}

// newBackupConfigCache returns a new instance of the backup config cache.
func newBackupConfigCache(backend *lxdBackend) *backupConfigCache {
	return &backupConfigCache{
		pools: map[string]Pool{
			// Initialize the cache with the already existing backend's pool.
			backend.name: backend,
		},
		volumes: map[string]map[string]map[string]*backupConfig.Volume{},
		state:   backend.state,
	}
}

// getPool returns the pool either by loading it from the DB or from the cache (preferred).
func (b *backupConfigCache) getPool(name string) (Pool, error) {
	// Load the pool if it cannot be found.
	_, ok := b.pools[name]
	if !ok {
		var err error

		// Custom volume's pool is not yet in the cache, load it.
		pool, err := LoadByName(b.state, name)
		if err != nil {
			return nil, err
		}

		// Cache the pool.
		b.pools[name] = pool
	}

	return b.pools[name], nil
}

// getVolume returns the volume's backup config either by loading it from the DB or from the cache (preferred).
func (b *backupConfigCache) getVolume(projectName string, poolName string, volName string, snapshots bool, op *operations.Operation) (*backupConfig.Volume, error) {
	// Create project cache.
	_, ok := b.volumes[projectName]
	if !ok {
		b.volumes[projectName] = map[string]map[string]*backupConfig.Volume{}
	}

	// Create pool cache.
	_, ok = b.volumes[projectName][poolName]
	if !ok {
		b.volumes[projectName][poolName] = map[string]*backupConfig.Volume{}
	}

	_, ok = b.volumes[projectName][poolName][volName]
	if !ok {
		pool, err := b.getPool(poolName)
		if err != nil {
			return nil, fmt.Errorf("Failed to retrieve pool of volume %q in pool %q: %w", volName, poolName, err)
		}

		volConfig, err := pool.GenerateCustomVolumeBackupConfig(projectName, volName, snapshots, op)
		if err != nil {
			// When restoring an instance from snapshot, some of the custom vols which were attached
			// whilst taking the snapshot might not exist anymore.
			// To not cause failures during restore, skip those volumes.
			// These volumes are still listed in the list of instance devices and will cause an error when
			// trying to start the instance.
			return nil, fmt.Errorf("Failed generating backup config for volume %q in project %q: %w", volName, projectName, err)
		}

		vol, err := volConfig.CustomVolume()
		if err != nil {
			return nil, fmt.Errorf("Failed getting the custom volume: %w", err)
		}

		// Cache the volume.
		b.volumes[projectName][poolName][volName] = vol
	}

	return b.volumes[projectName][poolName][volName], nil
}
