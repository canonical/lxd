package filters

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// errProjectFootprintExceeded is returned (wrapped) by FilterByProjectFootprint when a project's
// limits.max_hosts bound leaves no eligible candidates.
var errProjectFootprintExceeded = errors.New("No eligible cluster member within the project's host footprint limit")

// FilterByProjectFootprint narrows candidates to satisfy a project's limits.max_hosts bound
// (pctx.ProjectMaxHosts, for pctx.ProjectName) — the total distinct-host footprint across every
// instance in the project, grouped or not. This is independent of, and applied on top of, any
// placement-group-level restriction already narrowed onto the engine (via the placement-group
// stages, or no narrowing at all if the instance has no placement group).
//
// Enforcement is always strict, regardless of a placement group's own rigor — a project's
// limits.max_hosts is an administrative ceiling that an individual group's rigor=permissive can't
// opt out of.
//
// Only applies when pctx.ProjectMaxHosts is actually set: the limits.max_hosts config key
// validator requires a value >= 1, so 0 can only mean "not configured" and is a safe sentinel for
// "no limit" rather than a real, enforceable bound of zero.
func FilterByProjectFootprint(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	if pctx.ProjectMaxHosts == 0 {
		return candidates, nil
	}

	nodeToInst, err := cluster.GetInstancesInProject(ctx, tx.Tx(), pctx.ProjectName, pctx.ExcludeNodeID)
	if err != nil {
		return nil, err
	}

	usedHosts := sets.FromSeq(maps.Keys(nodeToInst))

	restricted, err := ResolveHostWithinBucket(api.PlacementRigorStrict, candidates, usedHosts, pctx.ProjectMaxHosts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errProjectFootprintExceeded, err)
	}

	return restricted, nil
}

// ResolveHostWithinBucket narrows candidates to those permitted by the max_hosts bounded
// host-footprint regime, given the current footprint: usedHosts, the set of host IDs already
// running this group's instances in the relevant bucket (cluster-wide for scope=host, or within
// one already-selected domain for scope=failure-domain). F, the footprint size, is len(usedHosts).
//
//   - F < maxHosts (below the cap): grow — restricted to candidates not already in usedHosts.
//     This is the unconditional default direction, regardless of policy.
//   - F >= maxHosts (at or above the cap): forced reuse — restricted to candidates already in
//     usedHosts, load-balanced (not piled onto whichever is busiest — that's what the final
//     least-loaded tie-break among whatever this returns achieves).
//
// Rigor governs fallback: strict fails if the regime's restriction can't be satisfied by a live
// candidate; permissive widens back to the full candidate set instead. This function does not
// itself pick a single host — the final tie-break among whatever it returns is
// [db.ClusterTx.GetNodeWithLeastInstances], same as the unbounded legacy path.
func ResolveHostWithinBucket(rigor string, candidates []db.NodeInfo, usedHosts sets.Set[int64], maxHosts int) ([]db.NodeInfo, error) {
	isUsed := func(c db.NodeInfo) bool { return usedHosts.Contains(c.ID) }

	if len(usedHosts) >= maxHosts {
		// At or above the cap: forced reuse.
		used := slices.Collect(iterutil.Filter(slices.Values(candidates), isUsed))
		if len(used) > 0 {
			return used, nil
		}

		if rigor == api.PlacementRigorStrict {
			return nil, errors.New("No eligible cluster members available to reuse within the placement group's footprint")
		}

		return candidates, nil
	}

	// Below the cap: grow.
	isUnused := func(c db.NodeInfo) bool { return !isUsed(c) }
	unused := slices.Collect(iterutil.Filter(slices.Values(candidates), isUnused))
	if len(unused) > 0 {
		return unused, nil
	}

	if rigor == api.PlacementRigorStrict {
		return nil, errors.New("No eligible cluster members available to grow the placement group's footprint")
	}

	return candidates, nil
}
