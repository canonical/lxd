package schema

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/lxd/db/query"
)

// Return whether the schema table is present in the database.
func doesSchemaTableExist(tx *sql.Tx) (bool, error) {
	statement := `
SELECT COUNT(name) FROM sqlite_master WHERE type = 'table' AND name = 'schema'
`
	rows, err := tx.Query(statement)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, fmt.Errorf("schema table query returned no rows")
	}

	var count int
	err = rows.Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// Return all versions in the schema table, in increasing order.
func selectSchemaVersions(tx *sql.Tx) ([]int, error) {
	statement := `
SELECT version FROM schema ORDER BY version
`
	return query.SelectIntegers(tx, statement)
}

// Return a list of SQL statements that can be used to create all tables in the
// database.
func selectTablesSQL(tx *sql.Tx) ([]string, error) {
	statement := `
SELECT sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema' ORDER BY name
`
	return query.SelectStrings(tx, statement)
}

// Create the schema table.
func createSchemaTable(tx *sql.Tx) error {
	statement := `
CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
)
`
	_, err := tx.Exec(statement)
	return err
}

// Insert a new version into the schema table.
func insertSchemaVersion(tx *sql.Tx, new int) error {
	statement := `
INSERT INTO schema (version, updated_at) VALUES (?, strftime("%s"))
`
	_, err := tx.Exec(statement, new)
	return err
}
