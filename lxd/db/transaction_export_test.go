package db

import "database/sql"

// Tx returns the low level database handle to the cluster transaction.
//
// FIXME: this is needed by tests that need to interact with entities that have
// no high-level ClusterTx APIs yet (containers, images, etc.).
func (c *ClusterTx) Tx() *sql.Tx {
	return c.tx
}
