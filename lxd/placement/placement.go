package placement

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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

// PlaceInstance is the placement package's sole entry point. Every stage below is always applied
// to one shared engine.PlacementEngine — each stage decides its own applicability internally from
// the models.PlacementContext fields PlaceInstance sets, rather than PlaceInstance wrapping
// conditional Apply calls around them:
//
//   - FilterByClusterGroup narrows to clusterGroupName's members, when placementGroup is unset.
//   - The placement-group chain (LoadPlacementGroupMembers, FilterByClusterFailureDomains,
//     FilterByPolicyAndRigor, FilterBySpreadWithinDomain) narrows by placementGroup's own
//     scope/policy/rigor, when set.
//   - FilterByProjectFootprint narrows to instProject's limits.max_hosts bound, when configured.
//   - SelectLeastLoaded always picks the single least-loaded member among whatever remains.
//
// instProject supplies both the project name and the limits.max_hosts bound (parsed from its
// Config here, rather than requiring every caller to parse it itself) — instProject.Config is
// never mutated. excludeNodeID is consulted only once project-footprint filtering is active.
// evacuation marks this placement decision as choosing an evacuation target rather than a
// create/migrate destination, so the placement-group stage excludes the source member from its own
// instance counting — instances about to move away from it shouldn't count against it.
//
// models.PlacementContext/engine.PlacementEngine are this function's own implementation detail —
// callers never construct or see them.
//
// PlaceInstance never returns an API error: on failure it returns ErrNoEligibleCandidate (check
// with errors.Is), wrapping the specific underlying reason. Converting that to a status code and a
// caller-appropriate message is entirely up to the caller.
func PlaceInstance(ctx context.Context, tx *db.ClusterTx, candidates []db.NodeInfo, placementGroup *api.PlacementGroup, clusterGroupName string, clusterFailureDomains []string, instProject api.Project, excludeNodeID *int64, evacuation bool) (*db.NodeInfo, error) {
	pctx := &models.PlacementContext{
		ClusterFailureDomains: clusterFailureDomains,
		ClusterGroupName:      clusterGroupName,
		ProjectName:           instProject.Name,
		ExcludeNodeID:         excludeNodeID,
		Evacuation:            evacuation,
	}

	if placementGroup != nil {
		pctx.PlacementGroup = *placementGroup
	}

	if raw := instProject.Config["limits.max_hosts"]; raw != "" {
		maxHosts, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("Invalid value for \"limits.max_hosts\": %w", err)
		}

		pctx.ProjectMaxHosts = maxHosts
	}

	selected, err := engine.New(ctx, tx, pctx, candidates).
		Apply(filters.FilterByClusterGroup).
		Apply(filters.LoadPlacementGroupMembers).
		Apply(filters.FilterByClusterFailureDomains).
		Apply(filters.FilterByPolicyAndRigor).
		Apply(filters.FilterBySpreadWithinDomain).
		Apply(filters.FilterByProjectFootprint).
		Apply(filters.SelectLeastLoaded).
		Result()
	if err != nil {
		return nil, fmt.Errorf("%w. (reason: %w)", ErrNoEligibleCandidate, err)
	}

	return &selected[0], nil
}
