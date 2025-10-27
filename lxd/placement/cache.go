package placement

import (
	"context"
	"sync"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
)

// Cache stores placement groups loaded within a short-lived scope (eg: a DB transaction) to avoid reloading the same placement group multiple times.
type Cache struct {
	mu    sync.Mutex
	items map[string]*cluster.PlacementGroup
}

// NewCache returns an initialized placement cache.
func NewCache() *Cache {
	return &Cache{
		items: make(map[string]*cluster.PlacementGroup),
	}
}

// Get returns the placement group for the given project/name, loading it via [cluster.GetPlacementGroup] if not present in the cache.
// The provided [*db.ClusterTx] is used to perform the DB call.
func (c *Cache) Get(ctx context.Context, tx *db.ClusterTx, name string, projectName string) (*cluster.PlacementGroup, error) {
	key := projectName + "/" + name

	c.mu.Lock()
	pg, ok := c.items[key]
	c.mu.Unlock()

	if ok {
		return pg, nil
	}

	// Load from DB using the provided transaction.
	dbPg, err := cluster.GetPlacementGroup(ctx, tx.Tx(), name, projectName)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.items[key] = dbPg
	c.mu.Unlock()

	return dbPg, nil
}
