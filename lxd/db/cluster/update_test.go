package cluster_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/stretchr/testify/require"
)

func TestUpdateFromV0(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(1, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'foo', 'blah', '1.2.3.4:666', 1, 32, ?)", time.Now())
	require.NoError(t, err)

	// Unique constraint on name
	_, err = db.Exec("INSERT INTO nodes VALUES (2, 'foo', 'gosh', '5.6.7.8:666', 5, 20, ?)", time.Now())
	require.Error(t, err)

	// Unique constraint on address
	_, err = db.Exec("INSERT INTO nodes VALUES (3, 'bar', 'gasp', '1.2.3.4:666', 9, 11), ?)", time.Now())
	require.Error(t, err)
}
