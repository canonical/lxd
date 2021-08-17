//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
)

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
	tx     *sql.Tx           // Handle to a transaction in the cluster dqlite database.
	nodeID int64             // Node ID of this LXD instance.
	stmts  map[int]*sql.Stmt // Prepared statements by code.
}

// NodeID sets the the node NodeID associated with this cluster transaction.
func (c *ClusterTx) NodeID(id int64) {
	c.nodeID = id
}

// GetNodeID gets the ID of the node associated with this cluster transaction.
func (c *ClusterTx) GetNodeID() int64 {
	return c.nodeID
}

func (c *ClusterTx) stmt(code int) *sql.Stmt {
	stmt, ok := c.stmts[code]
	if !ok {
		panic(fmt.Sprintf("No prepared statement registered with code %d", code))
	}
	return c.tx.Stmt(stmt)
}

// prepare prepares a new statement from a SQL string.
func (c *ClusterTx) prepare(sql string) (*sql.Stmt, error) {
	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, fmt.Errorf("Unable to prepare statement with error: %v", err)
	}

	return stmt, nil
}

// QueryScan runs a query with inArgs and provides the rowFunc with the scan function for each row.
// It handles closing the rows and errors from the result set.
func (c *ClusterTx) QueryScan(sql string, rowFunc func(scan func(dest ...interface{}) error) error, inArgs ...interface{}) error {
	rows, err := c.tx.Query(sql, inArgs...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		err = rowFunc(rows.Scan)
		if err != nil {
			return err
		}
	}

	return rows.Err()
}
