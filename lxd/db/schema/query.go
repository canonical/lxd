package schema

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"
)

// DoesSchemaTableExist return whether the schema table is present in the
// database.
func DoesSchemaTableExist(tx *sql.Tx) (bool, error) {
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
SELECT sql FROM sqlite_master WHERE
  type IN ('table', 'index', 'view', 'trigger') AND
  name != 'schema' AND
  name NOT LIKE 'sqlite_%'
ORDER BY name
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

// Read the given file (if it exists) and executes all queries it contains.
func execFromFile(tx *sql.Tx, path string, hook Hook) error {
	if !shared.PathExists(path) {
		return nil
	}

	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return errors.Wrap(err, "failed to read file")
	}

	if hook != nil {
		err := hook(-1, tx)
		if err != nil {
			return errors.Wrap(err, "failed to execute hook")
		}
	}

	_, err = tx.Exec(string(bytes))
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if err != nil {
		return errors.Wrap(err, "failed to remove file")
	}

	return nil
}
