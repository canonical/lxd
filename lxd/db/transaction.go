package db

import "database/sql"

// NodeTx models a single interaction with a LXD node-local database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type NodeTx struct {
	tx *sql.Tx // Handle to a transaction in the node-level SQLite database.
}

// Tx returns the low level database handle to the node-local SQLite
// transaction.
//
// FIXME: this is a transitional method needed for compatibility with some
//        legacy call sites. It should be removed when there are no more
//        consumers.
func (n *NodeTx) Tx() *sql.Tx {
	return n.tx
}

// ClusterTx models a single interaction with a LXD cluster database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type ClusterTx struct {
	tx     *sql.Tx // Handle to a transaction in the cluster dqlite database.
	nodeID int64   // Node ID of this LXD instance.
}

// NodeID sets the the node NodeID associated with this cluster transaction.
func (c *ClusterTx) NodeID(id int64) {
	c.nodeID = id
}
