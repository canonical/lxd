//go:build linux && cgo && !agent

package db

import (
	"database/sql"
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

// NodeID sets the the node NodeID associated with this cluster transaction.
func (c *ClusterTx) NodeID(id int64) {
	c.nodeID = id
}

// GetNodeID gets the ID of the node associated with this cluster transaction.
func (c *ClusterTx) GetNodeID() int64 {
	return c.nodeID
}

// QueryScan runs a query with inArgs and provides the rowFunc with the scan function for each row.
// It handles closing the rows and errors from the result set.
func (c *ClusterTx) QueryScan(sql string, rowFunc func(scan func(dest ...any) error) error, inArgs ...any) error {
	rows, err := c.tx.Query(sql, inArgs...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		err = rowFunc(rows.Scan)
		if err != nil {
			return err
		}
	}

	return rows.Err()
}
