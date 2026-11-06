package placement

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
)

// TestPlaceInstanceSkipsStagesWithNoInputs confirms a single live candidate is picked unchanged
// when placementGroup is nil.
func TestPlaceInstanceSkipsStagesWithNoInputs(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	var candidate db.NodeInfo
	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := tx.CreateNode("member01", "192.0.2.1")
		require.NoError(t, err)
		candidate = db.NodeInfo{ID: id, Name: "member01"}
		return nil
	})
	require.NoError(t, err)

	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		selected, err := PlaceInstance(ctx, tx, []db.NodeInfo{candidate}, nil, "", false)
		require.NoError(t, err)
		require.Equal(t, candidate, *selected)
		return nil
	})
	require.NoError(t, err)
}

// TestPlaceInstanceReturnsBareErrNoEligibleCandidate confirms a stage excluding every candidate
// surfaces only the generic ErrNoEligibleCandidate, with none of the stage-level detail attached.
func TestPlaceInstanceReturnsBareErrNoEligibleCandidate(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		notInGroup := []db.NodeInfo{{ID: 1, Name: "member01"}}
		_, err := PlaceInstance(ctx, tx, notInGroup, nil, "g1", false)
		require.ErrorIs(t, err, ErrNoEligibleCandidate)
		require.Equal(t, ErrNoEligibleCandidate.Error(), err.Error())
		return nil
	})
	require.NoError(t, err)
}

// TestPlaceInstanceEmptyCandidatesIsNotFound confirms an empty candidates list is reported as the
// least-loaded selection's own NotFound error, not as ErrNoEligibleCandidate: no stage excluded
// anything, so callers keep the status code they got before the placement stages existed.
func TestPlaceInstanceEmptyCandidatesIsNotFound(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := PlaceInstance(ctx, tx, nil, nil, "", false)
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound))
		require.NotErrorIs(t, err, ErrNoEligibleCandidate)
		return nil
	})
	require.NoError(t, err)
}

// TestPlaceInstanceFiltersByClusterGroup confirms the cluster-group stage narrows the candidates
// the least-loaded selection then chooses from.
func TestPlaceInstanceFiltersByClusterGroup(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	var inGroup, outOfGroup db.NodeInfo
	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := tx.CreateNode("member01", "192.0.2.1")
		require.NoError(t, err)
		inGroup = db.NodeInfo{ID: id, Name: "member01", Groups: []string{"g1"}}

		id, err = tx.CreateNode("member02", "192.0.2.2")
		require.NoError(t, err)
		outOfGroup = db.NodeInfo{ID: id, Name: "member02"}
		return nil
	})
	require.NoError(t, err)

	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		// outOfGroup is listed first so that a pass-through would pick it on the tie.
		selected, err := PlaceInstance(ctx, tx, []db.NodeInfo{outOfGroup, inGroup}, nil, "g1", false)
		require.NoError(t, err)
		require.Equal(t, inGroup.ID, selected.ID)
		return nil
	})
	require.NoError(t, err)
}

// TestPlaceInstanceEvacuationIgnoresSourceMemberInstances confirms that with evacuation set, the
// placement-group stage disregards the group's instances on the local (source) member, so a
// spread/strict group can place back onto it.
func TestPlaceInstanceEvacuationIgnoresSourceMemberInstances(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// The test cluster's local member is the pre-seeded node 1.
	var source db.NodeInfo
	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		source, err = tx.GetNodeByID(ctx, tx.GetNodeID())
		require.NoError(t, err)

		pgID, err := query.Create(ctx, tx.Tx(), cluster.PlacementGroupsRow{ProjectID: 1, Name: "pg1"})
		require.NoError(t, err)
		require.NoError(t, cluster.PlacementGroupsConfigStore().Set(ctx, tx.Tx(), pgID, map[string]string{
			"policy": api.PlacementPolicySpread,
			"rigor":  api.PlacementRigorStrict,
		}))

		instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Name: "c1", Node: source.Name, Project: "default", Type: instancetype.Container})
		require.NoError(t, err)
		require.NoError(t, cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{"placement.group": "pg1"}))
		return nil
	})
	require.NoError(t, err)

	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		pg, err := NewCache().Get(ctx, tx, "pg1", "default")
		require.NoError(t, err)

		// Not evacuating: the source already hosts an instance of the group, so spread/strict
		// excludes it and nothing remains.
		_, err = PlaceInstance(ctx, tx, []db.NodeInfo{source}, pg, "", false)
		require.ErrorIs(t, err, ErrNoEligibleCandidate)

		// Evacuating: that instance is about to leave the source, so the source is eligible again.
		selected, err := PlaceInstance(ctx, tx, []db.NodeInfo{source}, pg, "", true)
		require.NoError(t, err)
		require.Equal(t, source.ID, selected.ID)
		return nil
	})
	require.NoError(t, err)
}

// TestPlaceInstancePassesThroughNonPlacementFailures confirms a failure that isn't a placement
// outcome (here, a cancelled context aborting the least-loaded query) is returned as-is rather
// than being disguised as ErrNoEligibleCandidate.
func TestPlaceInstancePassesThroughNonPlacementFailures(t *testing.T) {
	testCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	var candidate db.NodeInfo
	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := tx.CreateNode("member01", "192.0.2.1")
		require.NoError(t, err)
		candidate = db.NodeInfo{ID: id, Name: "member01"}
		return nil
	})
	require.NoError(t, err)

	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		_, err := PlaceInstance(cancelled, tx, []db.NodeInfo{candidate}, nil, "", false)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrNoEligibleCandidate)
		require.ErrorIs(t, err, context.Canceled)
		return nil
	})
	require.NoError(t, err)
}
