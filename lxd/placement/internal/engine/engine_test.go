package engine

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

func TestPlacementEngineApplyChainsInOrder(t *testing.T) {
	candidates := []db.NodeInfo{{ID: 1}, {ID: 2}, {ID: 3}}

	// keepFirst and dropByID(1) only compose to a non-empty result in one order: dropping ID 1
	// first leaves [2, 3] for keepFirst to reduce to [2], whereas keepFirst first leaves [1] for
	// dropByID(1) to empty out.
	keepFirst := func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
		return candidates[:1], nil
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
		Apply(dropByID(1)).
		Apply(keepFirst).
		Result()
	require.NoError(t, err)
	require.Equal(t, &db.NodeInfo{ID: 2}, got)
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
	require.Equal(t, &candidates[0], got)
}

func TestPlacementEngineResultWithNoCandidatesIsNotFound(t *testing.T) {
	got, err := New(context.Background(), nil, &models.PlacementContext{}, nil).Result()
	require.True(t, api.StatusErrorCheck(err, http.StatusNotFound))
	require.Nil(t, got)
}
