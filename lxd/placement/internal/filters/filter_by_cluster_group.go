package filters

import (
	"context"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// FilterByClusterGroup narrows candidates to members of pctx.ClusterGroupName — the
// volatile.cluster.group target used when an instance has no placement group of its own. Only
// applies when pctx.ClusterGroupName is set and pctx.PlacementGroup isn't: a placement group's own
// scope/policy/rigor is used instead of cluster-group membership whenever one is present.
//
// If no candidate belongs to the group, this returns an empty slice rather than an error — the
// engine's terminal stage already fails with a clear "no candidates remain" error in that case,
// same as every other stage that can narrow candidates down to none.
func FilterByClusterGroup(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	if pctx.ClusterGroupName == "" || pctx.PlacementGroup.Name != "" {
		return candidates, nil
	}

	filtered := make([]db.NodeInfo, 0, len(candidates))
	for _, member := range candidates {
		if !slices.Contains(member.Groups, pctx.ClusterGroupName) {
			continue
		}

		filtered = append(filtered, member)
	}

	return filtered, nil
}
