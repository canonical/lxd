package placement

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
)

// Filter filters the provided slice of candidate cluster members using the provided [api.PlacementGroup].
func Filter(ctx context.Context, tx *db.ClusterTx, candidates []db.NodeInfo, apiPlacementGroup api.PlacementGroup, evacuation bool) ([]db.NodeInfo, error) {
	// Get policy and rigor from config.
	policy := apiPlacementGroup.Config["policy"]
	rigor := apiPlacementGroup.Config["rigor"]

	pgFilter := cluster.PlacementGroupFilter{Project: &apiPlacementGroup.Project, Name: &apiPlacementGroup.Name}

	// If this is an evacuation request, exclude instances on the source cluster member.
	// This allows placement decisions to be made based on where instances will be, not where they currently are.
	if evacuation {
		sourceMemberID := tx.GetNodeID()
		pgFilter.ExcludeMemberID = &sourceMemberID
	}

	memberToInst, err := cluster.GetInstancesInPlacementGroup(ctx, tx.Tx(), pgFilter)
	if err != nil {
		return nil, err
	}

	// Get compliant cluster members using the placement group.
	filteredCandidates, err := getCompliantMembers(policy, rigor, candidates, memberToInst)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusConflict, "Failed filtering candidate cluster members using placement group %q with %q policy and %q rigor: %w", apiPlacementGroup.Name, policy, rigor, err)
	}

	return filteredCandidates, nil
}

// getCompliantMembers gets compliant cluster members from the provided candidates based on the given placement policy and rigor.
func getCompliantMembers(policy string, rigor string, candidates []db.NodeInfo, memberToInst map[int][]int) ([]db.NodeInfo, error) {
	var compliantCandidates []db.NodeInfo

	switch {
	case policy == api.PlacementPolicySpread && rigor == api.PlacementRigorStrict:
		// Spread + Strict: Place at most one instance per cluster member.
		// Filter out candidates that already have instances.
		for _, c := range candidates {
			_, hasInst := memberToInst[int(c.ID)]
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
			counts = append(counts, len(memberToInst[int(c.ID)]))
		}

		minInstances := 0
		if len(counts) > 0 {
			minInstances = slices.Min(counts)
		}

		// Filter candidates to only those with at most minInstances instances.
		// This ensures the number of instances per cluster member differs by at most one.
		for _, c := range candidates {
			instanceCount := len(memberToInst[int(c.ID)])
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
		// The first instance determines the cluster member.
		if len(memberToInst) == 0 {
			// No instances yet.
			// All candidates are valid (first instance determines the member).
			return candidates, nil
		}

		// Find which member has instances from this placement group.
		var targetMemberID int
		for memberID := range memberToInst {
			targetMemberID = memberID
			break // All instances should be on same member in compact+strict.
		}

		// Filter candidates to only include the node that already has instances.
		for _, c := range candidates {
			if int(c.ID) == targetMemberID {
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

		// Find which member has instances from this placement group.
		var preferredMemberID int
		for memberID := range memberToInst {
			preferredMemberID = memberID
			break // All instances should be on same member ideally.
		}

		// Check if preferred member is in candidates.
		for _, c := range candidates {
			if int(c.ID) == preferredMemberID {
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
