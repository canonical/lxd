package filters

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// OrderByLeastLoaded reorders the candidates from least to most loaded, counting both the
// instances already on each member and those being created on it. It never excludes anything, and
// members with equal load keep their relative input order.
func OrderByLeastLoaded(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	counts, err := tx.GetNodesInstanceCounts(ctx, candidates)
	if err != nil {
		return nil, fmt.Errorf("OrderByLeastLoaded: counting the instances on %d candidates -> %w", len(candidates), err)
	}

	ordered := slices.Clone(candidates)
	slices.SortStableFunc(ordered, func(a, b db.NodeInfo) int {
		return cmp.Compare(counts[a.ID], counts[b.ID])
	})

	return ordered, nil
}
