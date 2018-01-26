package query_test

import (
	"database/sql"
	"testing"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise possible failure modes.
func TestSelectObjects_Error(t *testing.T) {
	cases := []struct {
		dest  query.Dest
		query string
		error string
	}{
		{
			func(int) []interface{} { return nil },
			"garbage",
			"near \"garbage\": syntax error",
		},
		{
			func(int) []interface{} { return make([]interface{}, 1) },
			"SELECT id, name FROM test",
			"sql: expected 2 destination arguments in Scan, not 1",
		},
	}
	for _, c := range cases {
		subtest.Run(t, c.query, func(t *testing.T) {
			tx := newTxForObjects(t)
			err := query.SelectObjects(tx, c.dest, c.query)
			assert.EqualError(t, err, c.error)
		})
	}
}

// Scan rows yielded by the query.
func TestSelectObjects(t *testing.T) {
	tx := newTxForObjects(t)
	objects := make([]struct {
		ID   int
		Name string
	}, 1)
	object := objects[0]

	dest := func(i int) []interface{} {
		require.Equal(t, 0, i, "expected at most one row to be yielded")
		return []interface{}{&object.ID, &object.Name}
	}

	stmt := "SELECT id, name FROM test WHERE name=?"
	err := query.SelectObjects(tx, dest, stmt, "bar")
	require.NoError(t, err)

	assert.Equal(t, 1, object.ID)
	assert.Equal(t, "bar", object.Name)
}

// Exercise possible failure modes.
func TestUpsertObject_Error(t *testing.T) {
	cases := []struct {
		columns []string
		values  []interface{}
		error   string
	}{
		{
			[]string{},
			[]interface{}{},
			"columns length is zero",
		},
		{
			[]string{"id"},
			[]interface{}{2, "egg"},
			"columns length does not match values length",
		},
	}
	for _, c := range cases {
		subtest.Run(t, c.error, func(t *testing.T) {
			tx := newTxForObjects(t)
			id, err := query.UpsertObject(tx, "foo", c.columns, c.values)
			assert.Equal(t, int64(-1), id)
			assert.EqualError(t, err, c.error)
		})
	}
}

// Insert a new row.
func TestUpsertObject_Insert(t *testing.T) {
	tx := newTxForObjects(t)

	id, err := query.UpsertObject(tx, "test", []string{"name"}, []interface{}{"egg"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), id)

	objects := make([]struct {
		ID   int
		Name string
	}, 1)
	object := objects[0]

	dest := func(i int) []interface{} {
		require.Equal(t, 0, i, "expected at most one row to be yielded")
		return []interface{}{&object.ID, &object.Name}
	}

	stmt := "SELECT id, name FROM test WHERE name=?"
	err = query.SelectObjects(tx, dest, stmt, "egg")
	require.NoError(t, err)

	assert.Equal(t, 2, object.ID)
	assert.Equal(t, "egg", object.Name)
}

// Update an existing row.
func TestUpsertObject_Update(t *testing.T) {
	tx := newTxForObjects(t)

	id, err := query.UpsertObject(tx, "test", []string{"id", "name"}, []interface{}{1, "egg"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	objects := make([]struct {
		ID   int
		Name string
	}, 1)
	object := objects[0]

	dest := func(i int) []interface{} {
		require.Equal(t, 0, i, "expected at most one row to be yielded")
		return []interface{}{&object.ID, &object.Name}
	}

	stmt := "SELECT id, name FROM test WHERE name=?"
	err = query.SelectObjects(tx, dest, stmt, "egg")
	require.NoError(t, err)

	assert.Equal(t, 1, object.ID)
	assert.Equal(t, "egg", object.Name)
}

// Exercise possible failure modes.
func TestDeleteObject_Error(t *testing.T) {
	tx := newTxForObjects(t)

	deleted, err := query.DeleteObject(tx, "foo", 1)
	assert.False(t, deleted)
	assert.EqualError(t, err, "no such table: foo")
}

// If an row was actually deleted, the returned flag is true.
func TestDeleteObject_Deleted(t *testing.T) {
	tx := newTxForObjects(t)

	deleted, err := query.DeleteObject(tx, "test", 1)
	assert.True(t, deleted)
	assert.NoError(t, err)
}

// If no row was actually deleted, the returned flag is false.
func TestDeleteObject_NotDeleted(t *testing.T) {
	tx := newTxForObjects(t)

	deleted, err := query.DeleteObject(tx, "test", 1000)
	assert.False(t, deleted)
	assert.NoError(t, err)
}

// Return a new transaction against an in-memory SQLite database with a single
// test table populated with a few rows for testing object-related queries.
func newTxForObjects(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test VALUES (0, 'foo'), (1, 'bar')")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}
