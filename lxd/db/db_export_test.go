package db

import "database/sql"

// DB returns the low level database handle to the cluster gRPC SQL database
// handler. Used by tests for introspecing the database with raw SQL.
func (c *Cluster) DB() *sql.DB {
	return c.db
}
