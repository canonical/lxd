package filters

import (
	"context"
	"errors"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// errNoCandidatesToSelectFrom is returned by SelectLeastLoaded when candidates is empty — every
// earlier stage already eliminated every cluster member, or none were live to begin with.
var errNoCandidatesToSelectFrom = errors.New("No cluster members remain to select the least-loaded one from")

// SelectLeastLoaded is the engine's terminal stage: it narrows whatever candidates remain after
// every earlier stage down to the single least-loaded cluster member, via
// [db.ClusterTx.GetNodeWithLeastInstances] — so PlaceInstance always returns exactly one node (or
// fails) rather than leaving the final tie-break to the caller.
func SelectLeastLoaded(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	if len(candidates) == 0 {
		return nil, errNoCandidatesToSelectFrom
	}

	selected, err := tx.GetNodeWithLeastInstances(ctx, candidates)
	if err != nil {
		return nil, err
	}

	return []db.NodeInfo{*selected}, nil
}
