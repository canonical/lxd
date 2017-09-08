package db

import "database/sql"

// NodeTx models a single interaction with a LXD node-local database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type NodeTx struct {
	tx *sql.Tx // Handle to a transaction in the node-level SQLite database.
}
