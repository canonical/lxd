package cluster

import (
	"database/sql"

	"github.com/lxc/lxd/lxd/db/schema"
)

// Schema for the cluster database.
func Schema() *schema.Schema {
	schema := schema.NewFromMap(updates)
	schema.Fresh(freshSchema)
	return schema
}

// SchemaDotGo refreshes the schema.go file in this package, using the updates
// defined here.
func SchemaDotGo() error {
	return schema.DotGo(updates, "schema")
}

// SchemaVersion is the current version of the cluster database schema.
var SchemaVersion = len(updates)

var updates = map[int]schema.Update{
	1: updateFromV0,
	2: updateFromV1,
}

func updateFromV1(tx *sql.Tx) error {
	// config table
	stmt := `
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (network_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE
);
`
	_, err := tx.Exec(stmt)
	return err
}
func updateFromV0(tx *sql.Tx) error {
	// v0..v1 the dawn of clustering
	stmt := `
CREATE TABLE nodes (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    address TEXT NOT NULL,
    schema INTEGER NOT NULL,
    api_extensions INTEGER NOT NULL,
    heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (name),
    UNIQUE (address)
);
`
	_, err := tx.Exec(stmt)
	return err
}
