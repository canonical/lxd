package placement

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/engine"
	"github.com/canonical/lxd/lxd/placement/internal/filters"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// ErrNoEligibleCandidate is returned (wrapped) by PlaceInstance whenever any FilterStage in its
// chain fails to find a candidate. Check with errors.Is; the wrapped error names which stage
// failed and why (e.g. "No eligible candidate cluster member. (reason: ...)").
var ErrNoEligibleCandidate = errors.New("No eligible candidate cluster member")

// PlaceInstance is the placement package's sole entry point: it narrows candidates by cluster
// group (clusterGroupName, only when placementGroup is unset) or by placement group (if set),
// then always picks the single least-loaded cluster member among whatever remains — every stage
// of the chain applied to one shared engine.PlacementEngine, each stage deciding its own
// applicability internally from the fields below rather than PlaceInstance wrapping conditional
// Apply calls around them. models.PlacementContext/engine.PlacementEngine are this function's own
// implementation detail — callers never construct or see them.
//
// evacuation marks this placement decision as choosing an evacuation target rather than a
// create/migrate destination, so the placement-group stage excludes the source member from its
// own instance counting — instances about to move away from it shouldn't count against it.
//
// PlaceInstance never returns an API error: on failure it returns ErrNoEligibleCandidate (check
// with errors.Is), wrapping the specific underlying reason. Converting that to a status code and a
// caller-appropriate message is entirely up to the caller.
func PlaceInstance(ctx context.Context, tx *db.ClusterTx, candidates []db.NodeInfo, placementGroup *api.PlacementGroup, clusterGroupName string, evacuation bool) (*db.NodeInfo, error) {
	pctx := &models.PlacementContext{ClusterGroupName: clusterGroupName, Evacuation: evacuation}

	if placementGroup != nil {
		pctx.PlacementGroup = *placementGroup
	}

	selected, err := engine.New(ctx, tx, pctx, candidates).
		Apply(filters.FilterByClusterGroup).
		Apply(filters.FilterByPlacementGroup).
		Apply(filters.SelectLeastLoaded).
		Result()
	if err != nil {
		return nil, fmt.Errorf("%w. (reason: %w)", ErrNoEligibleCandidate, err)
	}

	return &selected[0], nil
}
