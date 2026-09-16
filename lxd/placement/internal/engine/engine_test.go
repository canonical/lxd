package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

func TestPlacementEngineApplyChainsInOrder(t *testing.T) {
	candidates := []db.NodeInfo{{ID: 1}, {ID: 2}, {ID: 3}}

	keepEven := func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
		var kept []db.NodeInfo
		for _, c := range candidates {
			if c.ID%2 == 0 {
				kept = append(kept, c)
			}
		}

		return kept, nil
	}

	dropByID := func(id int64) FilterStage {
		return func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
			var kept []db.NodeInfo
			for _, c := range candidates {
				if c.ID != id {
					kept = append(kept, c)
				}
			}

			return kept, nil
		}
	}

	got, err := New(context.Background(), nil, &models.PlacementContext{}, candidates).
		Apply(keepEven).
		Apply(dropByID(2)).
		Result()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestPlacementEngineApplyShortCircuitsOnError(t *testing.T) {
	candidates := []db.NodeInfo{{ID: 1}}
	wantErr := errors.New("boom")

	fail := func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
		return nil, wantErr
	}

	ran := false
	neverRuns := func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
		ran = true
		return candidates, nil
	}

	got, err := New(context.Background(), nil, &models.PlacementContext{}, candidates).
		Apply(fail).
		Apply(neverRuns).
		Result()
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, got)
	require.False(t, ran, "a FilterStage after a failed one must never run")
}

func TestPlacementEngineResultWithNoFiltersApplied(t *testing.T) {
	candidates := []db.NodeInfo{{ID: 1}, {ID: 2}}

	got, err := New(context.Background(), nil, &models.PlacementContext{}, candidates).Result()
	require.NoError(t, err)
	require.Equal(t, candidates, got)
}
