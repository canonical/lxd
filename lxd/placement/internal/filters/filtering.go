package filters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// errNoEligiblePlacementGroupCandidate is returned (wrapped) by FilterByPlacementGroup when the
// group's own policy/rigor leaves no eligible candidates.
var errNoEligiblePlacementGroupCandidate = errors.New("No eligible candidate cluster member for the placement group")

// FilterByPlacementGroup filters the provided slice of candidate cluster members using
// pctx.PlacementGroup. Only applies when pctx.PlacementGroup is actually set (a real placement
// group's Name is never empty) — an instance with no placement group has nothing to filter by
// here.
func FilterByPlacementGroup(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error) {
	apiPlacementGroup := pctx.PlacementGroup
	if apiPlacementGroup.Name == "" {
		return candidates, nil
	}

	// Get policy and rigor from config.
	policy := apiPlacementGroup.Config["policy"]
	rigor := apiPlacementGroup.Config["rigor"]

	// If this is an evacuation request, exclude instances on the source cluster member.
	// This allows placement decisions to be made based on where instances will be, not where they currently are.
	var memberID *int64
	if pctx.Evacuation {
		sourceMemberID := tx.GetNodeID()
		memberID = &sourceMemberID
	}

	memberToInst, err := cluster.GetInstancesInPlacementGroup(ctx, tx.Tx(), apiPlacementGroup.Name, apiPlacementGroup.Project, memberID)
	if err != nil {
		return nil, err
	}

	// Get compliant cluster members using the placement group.
	filteredCandidates, err := getCompliantMembers(policy, rigor, candidates, memberToInst)
	if err != nil {
		return nil, fmt.Errorf("%w: policy %q, rigor %q: %w", errNoEligiblePlacementGroupCandidate, policy, rigor, err)
	}

	return filteredCandidates, nil
}

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

// getCompliantMembers gets compliant cluster members from the provided candidates based on the given placement policy and rigor.
func getCompliantMembers(policy string, rigor string, candidates []db.NodeInfo, memberToInst map[int64][]int64) ([]db.NodeInfo, error) {
	var compliantCandidates []db.NodeInfo

	switch {
	case policy == api.PlacementPolicySpread && rigor == api.PlacementRigorStrict:
		// Spread + Strict: Place at most one instance per cluster member.
		// Filter out candidates that already have instances.
		for _, c := range candidates {
			_, hasInst := memberToInst[c.ID]
			if !hasInst {
				compliantCandidates = append(compliantCandidates, c)
			}
		}

		if len(compliantCandidates) == 0 {
			return nil, errors.New("No eligible cluster members available")
		}

		return compliantCandidates, nil

	case policy == api.PlacementPolicySpread && rigor == api.PlacementRigorPermissive:
		// Spread + Permissive: Prefer spreading instances evenly across cluster members.
		// The number of instances per cluster member differs by at most one.

		// Find the minimum instance count among candidates.
		counts := make([]int, 0, len(candidates))
		for _, c := range candidates {
			counts = append(counts, len(memberToInst[c.ID]))
		}

		minInstances := 0
		if len(counts) > 0 {
			minInstances = slices.Min(counts)
		}

		// Filter candidates to only those with at most minInstances instances.
		// This ensures the number of instances per cluster member differs by at most one.
		for _, c := range candidates {
			instanceCount := len(memberToInst[c.ID])
			if instanceCount <= minInstances {
				compliantCandidates = append(compliantCandidates, c)
			}
		}

		if len(compliantCandidates) == 0 {
			return nil, errors.New("No eligible cluster members available")
		}

		return compliantCandidates, nil

	case policy == api.PlacementPolicyCompact && rigor == api.PlacementRigorStrict:
		// Compact + Strict: Place all instances on the same cluster member.
		// The member with the most instances determines the cluster member.
		if len(memberToInst) == 0 {
			// No instances yet.
			// All candidates are valid (first instance determines the member).
			return candidates, nil
		}

		// Find which member has the most instances from this placement group.
		var targetMemberID int64
		maxInstances := -1
		for memberID, instances := range memberToInst {
			if len(instances) > maxInstances {
				maxInstances = len(instances)
				targetMemberID = memberID
			}
		}

		// Filter candidates to only include the node with the most instances.
		for _, c := range candidates {
			if c.ID == targetMemberID {
				compliantCandidates = append(compliantCandidates, c)
				break
			}
		}

		if len(compliantCandidates) == 0 {
			return nil, errors.New("Required cluster member is unavailable")
		}

		return compliantCandidates, nil

	case policy == api.PlacementPolicyCompact && rigor == api.PlacementRigorPermissive:
		// Compact + Permissive: Prefer to place all instances on the same cluster member.
		if len(memberToInst) == 0 {
			// No instances yet.
			// All candidates are valid (first instance determines preferred member).
			return candidates, nil
		}

		// Find which member has the most instances from this placement group.
		var preferredMemberID int64
		maxInstances := -1
		for memberID, instances := range memberToInst {
			if len(instances) > maxInstances {
				maxInstances = len(instances)
				preferredMemberID = memberID
			}
		}

		// Check if preferred member is in candidates.
		for _, c := range candidates {
			if c.ID == preferredMemberID {
				// Preferred member is available.
				return []db.NodeInfo{c}, nil
			}
		}

		// Preferred node is not available - fall back to all candidates.
		return candidates, nil

	default:
		return nil, errors.New("Invalid placement group")
	}
}
