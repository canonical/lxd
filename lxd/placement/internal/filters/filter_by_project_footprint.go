package filters

import (
	"errors"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
	"github.com/canonical/lxd/shared/api"
)

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
