package storage

import (
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/state"
)

// backupConfigCache is used to cache pools and volumes during backup config creation.
type backupConfigCache struct {
	pools map[string]Pool
	// The volume cache is using the pool as its first dimension.
	// By default all projects use features.storage.volumes=true which uses the volumes from the individual project.
	// In this case the top level dimension only has an entry for the pool(s) which causes the cache to stay small.
	// Having the project as the top level dimension would cause a duplicated pool entry under every project from which volumes are consumed:
	// {
	//  "pool1": {
	//   "default": {<vols>},
	//   "project1": {<vols>},
	//   ...
	//  },
	//  ...
	// }
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
