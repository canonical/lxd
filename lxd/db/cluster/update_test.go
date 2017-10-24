package cluster_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateFromV0(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(1, nil)
	require.NoError(t, err)

	stmt := "INSERT INTO nodes VALUES (1, 'foo', 'blah', '1.2.3.4:666', 1, 32, ?)"
	_, err = db.Exec(stmt, time.Now())
	require.NoError(t, err)

	// Unique constraint on name
	stmt = "INSERT INTO nodes VALUES (2, 'foo', 'gosh', '5.6.7.8:666', 5, 20, ?)"
	_, err = db.Exec(stmt, time.Now())
	require.Error(t, err)

	// Unique constraint on address
	stmt = "INSERT INTO nodes VALUES (3, 'bar', 'gasp', '1.2.3.4:666', 9, 11), ?)"
	_, err = db.Exec(stmt, time.Now())
	require.Error(t, err)
}

func TestUpdateFromV1_Config(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO config VALUES (1, 'foo', 'blah')")
	require.NoError(t, err)

	// Unique constraint on key.
	_, err = db.Exec("INSERT INTO config VALUES (2, 'foo', 'gosh')")
	require.Error(t, err)
}

func TestUpdateFromV1_Network(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO networks VALUES (1, 'foo', 'blah', 1)")
	require.NoError(t, err)

	// Unique constraint on name.
	_, err = db.Exec("INSERT INTO networks VALUES (2, 'foo', 'gosh', 1)")
	require.Error(t, err)
}

func TestUpdateFromV1_ConfigTables(t *testing.T) {
	testConfigTable(t, "networks", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO networks VALUES (1, 'foo', 'blah', 1)")
		require.NoError(t, err)
	})
	testConfigTable(t, "storage_pools", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO storage_pools VALUES (1, 'default', 'dir', '')")
		require.NoError(t, err)
	})
	testConfigTable(t, "storage_volumes", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO storage_pools VALUES (1, 'default', 'dir', '')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'dev', 1, 1, '')")
		require.NoError(t, err)
	})
}

func testConfigTable(t *testing.T, table string, setup func(db *sql.DB)) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (2, 'two', '', '2.2.2.2', 666, 999, ?)", time.Now())
	require.NoError(t, err)

	stmt := func(format string) string {
		return fmt.Sprintf(format, table)
	}

	setup(db)

	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (1, 1, 1, 'bar', 'baz')"))
	require.NoError(t, err)

	// Unique constraint on <entity>_id/node_id/key.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (2, 1, 1, 'bar', 'egg')"))
	require.Error(t, err)
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (3, 1, 2, 'bar', 'egg')"))
	require.NoError(t, err)

	// Reference constraint on <entity>_id.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (4, 2, 1, 'fuz', 'buz')"))
	require.Error(t, err)

	// Reference constraint on node_id.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (5, 1, 3, 'fuz', 'buz')"))
	require.Error(t, err)

	// Cascade deletes on node_id
	result, err := db.Exec("DELETE FROM nodes WHERE id=2")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	result, err = db.Exec(stmt("UPDATE %s_config SET value='yuk'"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n) // Only one row was affected, since the other got deleted

	// Cascade deletes on <entity>_id
	result, err = db.Exec(stmt("DELETE FROM %s"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	result, err = db.Exec(stmt("DELETE FROM %s_config"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n) // The row was already deleted by the previous query
}
