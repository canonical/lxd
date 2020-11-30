package query_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/subtest"
)

func TestSelectURIs(t *testing.T) {
	tx := newTxForSlices(t)
	defer tx.Rollback()

	stmt, err := tx.Prepare("SELECT id, name FROM test ORDER BY name")
	require.NoError(t, err)
	defer stmt.Close()

	uris, err := query.SelectURIs(stmt, func(a ...interface{}) string {
		return fmt.Sprintf("/1.0/test/%s/%d", a[1], a[0])
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"/1.0/test/bar/1", "/1.0/test/foo/0"}, uris)
}

// Exercise possible failure modes.
func TestStrings_Error(t *testing.T) {
	for _, c := range testStringsErrorCases {
		subtest.Run(t, c.query, func(t *testing.T) {
			tx := newTxForSlices(t)
			values, err := query.SelectStrings(tx, c.query)
			assert.EqualError(t, err, c.error)
			assert.Nil(t, values)
		})
	}
}

var testStringsErrorCases = []struct {
	query string
	error string
}{
	{"garbage", "near \"garbage\": syntax error"},
	{"SELECT id, name FROM test", "sql: expected 2 destination arguments in Scan, not 1"},
}

// All values yield by the query are returned.
func TestStrings(t *testing.T) {
	tx := newTxForSlices(t)
	values, err := query.SelectStrings(tx, "SELECT name FROM test ORDER BY name")
	assert.Nil(t, err)
	assert.Equal(t, []string{"bar", "foo"}, values)
}

// Exercise possible failure modes.
func TestIntegers_Error(t *testing.T) {
	for _, c := range testIntegersErrorCases {
		subtest.Run(t, c.query, func(t *testing.T) {
			tx := newTxForSlices(t)
			values, err := query.SelectIntegers(tx, c.query)
			assert.EqualError(t, err, c.error)
			assert.Nil(t, values)
		})
	}
}

var testIntegersErrorCases = []struct {
	query string
	error string
}{
	{"garbage", "near \"garbage\": syntax error"},
	{"SELECT id, name FROM test", "sql: expected 2 destination arguments in Scan, not 1"},
}

// All values yield by the query are returned.
func TestIntegers(t *testing.T) {
	tx := newTxForSlices(t)
	values, err := query.SelectIntegers(tx, "SELECT id FROM test ORDER BY id")
	assert.Nil(t, err)
	assert.Equal(t, []int{0, 1}, values)
}

// Insert new rows in bulk.
func TestInsertStrings(t *testing.T) {
	tx := newTxForSlices(t)

	err := query.InsertStrings(tx, "INSERT INTO test(name) VALUES %s", []string{"xx", "yy"})
	require.NoError(t, err)

	values, err := query.SelectStrings(tx, "SELECT name FROM test ORDER BY name DESC LIMIT 2")
	require.NoError(t, err)
	assert.Equal(t, values, []string{"yy", "xx"})
}

// Return a new transaction against an in-memory SQLite database with a single
// test table populated with a few rows.
func newTxForSlices(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test (id INTEGER, name TEXT)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test VALUES (0, 'foo'), (1, 'bar')")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}
