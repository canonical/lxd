package query_test

import (
	"database/sql"
	"strconv"
	"testing"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Count returns the current number of rows.
func TestCount(t *testing.T) {
	cases := []struct {
		where string
		args  []interface{}
		count int
	}{
		{
			"id=?",
			[]interface{}{999},
			0,
		},
		{
			"id=?",
			[]interface{}{1},
			1,
		},
		{
			"",
			[]interface{}{},
			2,
		},
	}
	for _, c := range cases {
		subtest.Run(t, strconv.Itoa(c.count), func(t *testing.T) {
			tx := newTxForCount(t)
			count, err := query.Count(tx, "test", c.where, c.args...)
			require.NoError(t, err)
			assert.Equal(t, c.count, count)
		})
	}
}

// Return a new transaction against an in-memory SQLite database with a single
// test table and a few rows.
func newTxForCount(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test (id INTEGER)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test VALUES (1), (2)")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}
