package placement

import (
	"context"
	"errors"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/engine"
	"github.com/canonical/lxd/lxd/placement/internal/filters"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// ErrNoEligibleCandidate is returned by PlaceInstance when its FilterStages legitimately leave no
// candidate cluster member. It carries no detail on purpose: the specific reason is logged as a
// warning rather than surfaced to the API.
var ErrNoEligibleCandidate = errors.New("No eligible candidate cluster member")

// PlaceInstance narrows candidates by cluster group or placement group, then picks the least-loaded
// cluster member among whatever remains. A stage excluding every candidate returns
// ErrNoEligibleCandidate (check with errors.Is); any other failure is returned as-is, including
// the NotFound error reported for an empty candidates list.
func PlaceInstance(ctx context.Context, tx *db.ClusterTx, candidates []db.NodeInfo, placementGroup *api.PlacementGroup, clusterGroupName string, evacuation bool) (*db.NodeInfo, error) {
	pctx := &models.PlacementContext{ClusterGroupName: clusterGroupName, Evacuation: evacuation}

	if placementGroup != nil {
		pctx.PlacementGroup = *placementGroup
	}

	selected, err := engine.New(ctx, tx, pctx, candidates).
		Apply(filters.FilterByClusterGroup).
		Apply(filters.FilterByPlacementGroup).
		Apply(filters.OrderByLeastLoaded).
		Result()
	if err != nil {
		var noCandidates *filters.NoCandidatesError
		if !errors.As(err, &noCandidates) {
			return nil, err
		}

		logger.Warn("No eligible candidate cluster member", logger.Ctx{"placementGroup": pctx.PlacementGroup.Name, "clusterGroup": clusterGroupName, "candidates": len(candidates), "err": err})
		return nil, ErrNoEligibleCandidate
	}

	return selected, nil
}
