//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
)

// newTestClusterWithProject returns a test cluster holding a project named "immediate" described as "created".
func newTestClusterWithProject(t *testing.T) *db.Cluster {
	t.Helper()

	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)

	err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "immediate", Description: "created"})
		return err
	})
	require.NoError(t, err)

	return c
}

// projectDescription reads the "immediate" project's description in a regular transaction.
func projectDescription(t *testing.T, c *db.Cluster) string {
	t.Helper()

	var description string
	err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		project, err := cluster.GetProject(ctx, tx.Tx(), "immediate")
		if err != nil {
			return err
		}

		description = project.Description
		return nil
	})
	require.NoError(t, err)

	return description
}

// updateDescription sets the "immediate" project's description inside an immediate transaction, reading it back
// through the generated mappers.
func updateDescription(ctx context.Context, t *testing.T, tx *db.ImmediateClusterTx, description string) {
	t.Helper()

	id, err := cluster.GetProjectID(ctx, tx.Tx(), "immediate")
	require.NoError(t, err)

	_, err = tx.Tx().ExecContext(ctx, "UPDATE projects SET description = ? WHERE id = ?", description, id)
	require.NoError(t, err)

	// Reads inside the transaction see its own write.
	project, err := cluster.GetProject(ctx, tx.Tx(), "immediate")
	require.NoError(t, err)
	assert.Equal(t, description, project.Description)
	assert.NotZero(t, tx.GetNodeID())
}

// Generated mappers run inside an immediate transaction and its writes are visible after it commits.
func TestCluster_TransactionImmediate_Commit(t *testing.T) {
	c := newTestClusterWithProject(t)

	err := c.TransactionImmediate(context.Background(), func(ctx context.Context, tx *db.ImmediateClusterTx) error {
		updateDescription(ctx, t, tx, "updated")
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, "updated", projectDescription(t, c))
}

// An error from the function rolls the immediate transaction back.
func TestCluster_TransactionImmediate_Rollback(t *testing.T) {
	c := newTestClusterWithProject(t)
	boom := errors.New("boom")

	err := c.TransactionImmediate(context.Background(), func(ctx context.Context, tx *db.ImmediateClusterTx) error {
		updateDescription(ctx, t, tx, "discarded")
		return boom
	})
	require.ErrorIs(t, err, boom)

	assert.Equal(t, "created", projectDescription(t, c))
}
