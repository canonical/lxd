//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
)

// NodeTx models a single interaction with a LXD node-local database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type NodeTx struct {
	tx *sql.Tx // Handle to a transaction in the node-level SQLite database.
}

// ClusterTx models a single interaction with a LXD cluster database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type ClusterTx struct {
	tx     *sql.Tx // Handle to a transaction in the cluster dqlite database.
	nodeID int64   // Node ID of this LXD instance.
}

// Tx retrieves the underlying transaction on the cluster database.
func (c *ClusterTx) Tx() *sql.Tx {
	return c.tx
}

// NodeID sets the node NodeID associated with this cluster transaction.
func (c *ClusterTx) NodeID(id int64) {
	c.nodeID = id
}

// GetNodeID gets the ID of the node associated with this cluster transaction.
func (c *ClusterTx) GetNodeID() int64 {
	return c.nodeID
}

// ImmediateClusterTx is a cluster transaction that took the database write lock at BEGIN.
type ImmediateClusterTx struct {
	tx     *query.ImmediateTx
	nodeID int64
}

// Tx returns the statement API of the transaction.
func (c *ImmediateClusterTx) Tx() query.Executor {
	return c.tx
}

// GetNodeID returns the ID of the member that runs the transaction.
func (c *ImmediateClusterTx) GetNodeID() int64 {
	return c.nodeID
}

// InstancesToInstanceArgsWithoutProfiles converts instances to InstanceArgs with their config and devices; Profiles stays unset.
func (c *ImmediateClusterTx) InstancesToInstanceArgsWithoutProfiles(ctx context.Context, instances ...cluster.Instance) (map[int]InstanceArgs, error) {
	return instancesToInstanceArgsWithoutProfiles(ctx, c.tx, instances...)
}
