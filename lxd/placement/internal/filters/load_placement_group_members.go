package filters

import (
	"context"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// LoadPlacementGroupMembers fetches this placement group's current per-member instance counts
// (pctx.MemberToInst) and, if needed, each candidate's assigned failure domain
// (pctx.MemberDomains) once, up front, for every later placement-group stage to reuse rather than
// re-querying. Domains are only fetched when something later actually consults them: the
// cluster-wide allow-list is in effect, or the group buckets by domain instead of by member.
//
// Only applies when pctx.PlacementGroup is actually set (a real placement group's Name is never
// empty) — an instance with no placement group has nothing here to load.
//
// If this is an evacuation request, instances on the source cluster member are excluded from
// memberToInst. This allows placement decisions to be made based on where instances will be, not
// where they currently are.
func LoadPlacementGroupMembers(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	apiPlacementGroup := pctx.PlacementGroup
	if apiPlacementGroup.Name == "" {
		return candidates, nil
	}

	scope := apiPlacementGroup.Config["scope"]

	var memberID *int64
	if pctx.Evacuation {
		sourceMemberID := tx.GetNodeID()
		memberID = &sourceMemberID
	}

	memberToInst, err := cluster.GetInstancesInPlacementGroup(ctx, tx.Tx(), apiPlacementGroup.Name, apiPlacementGroup.Project, memberID)
	if err != nil {
		return nil, err
	}

	pctx.MemberToInst = memberToInst

	if scope == api.PlacementScopeFailureDomain || len(pctx.ClusterFailureDomains) > 0 {
		pctx.MemberDomains, err = models.LoadMemberFailureDomains(ctx, tx)
		if err != nil {
			return nil, err
		}
	}

	return candidates, nil
}
