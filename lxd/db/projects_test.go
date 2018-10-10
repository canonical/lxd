package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectsList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	project, err := tx.ProjectGet("default")
	require.NoError(t, err)

	assert.Len(t, project.UsedBy, 1)
	assert.Equal(t, "/1.0/profiles/default?project=default", project.UsedBy[0])
}
