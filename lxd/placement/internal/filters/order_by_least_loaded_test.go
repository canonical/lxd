package filters_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/placement/internal/filters"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

func TestOrderByLeastLoaded(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// Members named by how many instances they host; twoA and twoB are tied.
	var zero, one, twoA, twoB db.NodeInfo
	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		create := func(name string, instances int) db.NodeInfo {
			id, err := tx.CreateNode(name, "192.0.2."+name)
			require.NoError(t, err)

			for i := range instances {
				_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Name: name + "-c" + string(rune('a'+i)), Node: name, Project: "default", Type: instancetype.Container})
				require.NoError(t, err)
			}

			return db.NodeInfo{ID: id, Name: name}
		}

		zero, one, twoA, twoB = create("0", 0), create("1", 1), create("2", 2), create("3", 2)
		return nil
	})
	require.NoError(t, err)

	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Ascending by load; the tied pair keeps its input order (twoB before twoA), matching the
		// first-wins tie-break GetNodeWithLeastInstances applied before this stage existed.
		got, err := filters.OrderByLeastLoaded(ctx, tx, &models.PlacementContext{}, []db.NodeInfo{twoB, one, twoA, zero})
		require.NoError(t, err)
		require.Equal(t, []db.NodeInfo{zero, one, twoB, twoA}, got)

		// An empty list is simply returned empty: ordering never excludes anything.
		got, err = filters.OrderByLeastLoaded(ctx, tx, &models.PlacementContext{}, nil)
		require.NoError(t, err)
		require.Empty(t, got)
		return nil
	})
	require.NoError(t, err)
}
