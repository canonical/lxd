package query_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/stretchr/testify/assert"
)

// Any error happening when beginning the transaction will be propagated.
func TestTransaction_BeginError(t *testing.T) {
	db := newDB(t)
	db.Close()

	err := query.Transaction(db, func(*sql.Tx) error { return nil })
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to begin transaction")
}

// Any error happening when in the transaction function will cause a rollback.
func TestTransaction_FunctionError(t *testing.T) {
	db := newDB(t)

	err := query.Transaction(db, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE test (id INTEGER)")
		assert.NoError(t, err)
		return fmt.Errorf("boom")

	})
	assert.EqualError(t, err, "boom")

	tx, err := db.Begin()
	assert.NoError(t, err)

	tables, err := query.SelectStrings(tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	assert.NoError(t, err)
	assert.NotContains(t, tables, "test")
}

// Return a new in-memory SQLite database.
func newDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)
	return db
}
